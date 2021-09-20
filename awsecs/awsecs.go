package awsecs

import (
	"context"
	stderrors "errors"
	"fmt"
	"log"
	"regexp"
	"strings"

	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/goutils/color"
	"github.com/aws/aws-sdk-go-v2/aws/arn"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/pkg/errors"
)

// ErrHealthcheckFailed indicates that a ECS task failed a container healthcheck.
var ErrHealthcheckFailed = stderrors.New("health check failed")

type ECSClient interface {
	DescribeServices(ctx context.Context, params *ecs.DescribeServicesInput, optFns ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error)
	UpdateService(ctx context.Context, params *ecs.UpdateServiceInput, optFns ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error)
	ListTasks(ctx context.Context, params *ecs.ListTasksInput, optFns ...func(*ecs.Options)) (*ecs.ListTasksOutput, error)
	DescribeTasks(ctx context.Context, params *ecs.DescribeTasksInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error)
	DescribeTaskDefinition(ctx context.Context, params *ecs.DescribeTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error)
	RegisterTaskDefinition(ctx context.Context, params *ecs.RegisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error)
}

// Deploy registers a new task for the given service in ECS in order to create a new deployment.
func Deploy(ctx context.Context, service *config.Service, ecsClient ECSClient) error {
	// Ensure we've been passed a valid cluster ARN and exit if not
	clusterArn, err := arn.Parse(service.Cluster)
	if err != nil {
		return errors.Wrapf(err, "invalid cluster ARN: %s", service.Cluster)
	}
	log.Printf("Using cluster: %s\n", clusterArn)

	// Retrieve existing service config
	log.Printf("Checking for service: %s\n", color.Cyan(service.Name))
	respDescribeServices, err := ecsClient.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Services: []string{service.Name},
		Cluster:  &service.Cluster,
	})
	if err != nil {
		return errors.Wrapf(err, "failed to find service: %s", service.Name)
	}

	taskDefARN := *respDescribeServices.Services[0].TaskDefinition
	log.Printf("Found current task definition: %v\n", taskDefARN)

	updateTaskDefRes, err := updateTaskDef(ctx, taskDefARN, service.Gitsha, service.UpdateStrategy, service.Containers, ecsClient)
	if err != nil {
		return errors.Wrapf(err, "failed to update task def for service: %s", service.Name)
	}
	log.Printf("Updating service %s\n", color.Cyan(service.Name))

	// Set dynamic service values
	// Save previous Git SHA in case we need to rollback later
	service.PreviousGitsha = updateTaskDefRes.previousGitsha
	service.PreviousTaskDefinitionARN = taskDefARN
	service.TaskDefinitionARN = updateTaskDefRes.newTaskDefARN
	service.Tags = updateTaskDefRes.dockerTags
	if err := UpdateService(ctx, service, ecsClient); err != nil {
		return errors.Wrap(err, "failed to update service")
	}
	return nil
}

// UpdateService creates a new deployment on ECS.
func UpdateService(ctx context.Context, service *config.Service, ecsClient ECSClient) error {
	_, err := ecsClient.UpdateService(ctx, &ecs.UpdateServiceInput{
		TaskDefinition:     &service.TaskDefinitionARN,
		Service:            &service.Name,
		Cluster:            &service.Cluster,
		ForceNewDeployment: true,
	})
	if err != nil {
		return errors.Wrapf(err, "failed to update service %s in ECS", service.Name)
	}
	return nil
}

// CheckDrain checks if all old tasks have drained. If the tasks are failing healthchecks,
// the return error will wrap ErrHealthcheckFailed.
func CheckDrain(ctx context.Context, service *config.Service, ecsClient ECSClient) (bool, error) {
	respDescribeServices, err := ecsClient.DescribeServices(ctx, &ecs.DescribeServicesInput{
		Services: []string{service.Name},
		Cluster:  &service.Cluster,
	})
	if err != nil {
		return false, errors.Wrapf(err, "failed to get current service: %s", service.Name)
	}
	if len(respDescribeServices.Failures) > 0 {
		var sb strings.Builder
		for _, f := range respDescribeServices.Failures {
			writeFailure(&sb, f)
		}
		return false, errors.Wrapf(err, "failed to get service: %s", sb.String())
	}
	if len(respDescribeServices.Services) != 1 {
		return false, errors.Wrapf(err, "expected 1 service named %s, got %d", service.Name, len(respDescribeServices.Services))
	}

	awsService := respDescribeServices.Services[0]
	for _, deployment := range awsService.Deployments {
		expectedTaskDefARN := service.TaskDefinitionARN
		if expectedTaskDefARN == "" {
			// If no task def arn set for the service, fallback to the one present on AWS
			// this should work since that is set by calls to UpdateService, so it should be
			// the desired one for new deploys
			expectedTaskDefARN = *awsService.TaskDefinition
		}

		if (*deployment.TaskDefinition == expectedTaskDefARN) && (*deployment.Status == "PRIMARY") && (deployment.RunningCount == deployment.DesiredCount) {
			return true, nil
		}
	}

	// Check and see if container healthchecks failed so we can provide more details

	// TODO(@cszatmary): The response could be paginated, may need to handle this in the future
	respListTasks, err := ecsClient.ListTasks(ctx, &ecs.ListTasksInput{
		Cluster:     &service.Cluster,
		ServiceName: &service.Name,
	})
	if err != nil {
		return false, errors.Wrapf(err, "failed to list tasks for service: %s", service.Name)
	}
	respDescribeTasks, err := ecsClient.DescribeTasks(ctx, &ecs.DescribeTasksInput{
		Cluster: &service.Cluster,
		Tasks:   respListTasks.TaskArns,
	})
	if err != nil {
		return false, errors.Wrapf(err, "failed to get tasks for service: %s", service.Name)
	}
	if len(respDescribeTasks.Failures) > 0 {
		var sb strings.Builder
		for _, f := range respDescribeServices.Failures {
			writeFailure(&sb, f)
		}
		return false, errors.Wrapf(err, "failed to get tasks: %s", sb.String())
	}

	for _, task := range respDescribeTasks.Tasks {
		if task.HealthStatus == ecstypes.HealthStatusUnhealthy && *task.TaskDefinitionArn == service.TaskDefinitionARN {
			return false, errors.Wrapf(ErrHealthcheckFailed, "task %s is unhealthy", *task.TaskArn)
		}
	}
	return false, nil
}

type updateTaskDefResult struct {
	newTaskDefARN  string
	previousGitsha string
	dockerTags     []string
}

// updateTaskDef creates a new task def revision with the container image updated to use the new Git SHA.
// It returns the new ARN and previous Git SHA.
func updateTaskDef(ctx context.Context, taskDefARN, gitsha, updateStrategy string, containers []string, ecsClient ECSClient) (updateTaskDefResult, error) {
	taskDefName := taskDefARN
	if updateStrategy == config.UpdateStrategyLatest {
		// If latest parse the family name from the ARN so we can look up the latest revision
		// parse arn for family name
		r := regexp.MustCompile(`arn:aws:ecs:[^:\n]*:[^:\n]*:task-definition\/([^:\n]*):\d+`)
		matches := r.FindStringSubmatch(taskDefARN)
		if matches == nil {
			return updateTaskDefResult{}, errors.Errorf("unable to parse task def family: %s", taskDefARN)
		}
		taskDefName = matches[1]
	}

	// Use resolved resource info to grab existing task def
	respDescribeTaskDef, err := ecsClient.DescribeTaskDefinition(ctx, &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &taskDefName,
	})
	if err != nil {
		return updateTaskDefResult{}, errors.Wrapf(err, "failed to get task definition: %s", taskDefName)
	}

	// Convert API output to be ready to update task.
	taskDef := respDescribeTaskDef.TaskDefinition
	newTaskInput := ecs.RegisterTaskDefinitionInput{
		ContainerDefinitions:    taskDef.ContainerDefinitions,
		Cpu:                     taskDef.Cpu,
		ExecutionRoleArn:        taskDef.ExecutionRoleArn,
		Family:                  taskDef.Family,
		IpcMode:                 taskDef.IpcMode,
		Memory:                  taskDef.Memory,
		NetworkMode:             taskDef.NetworkMode,
		PidMode:                 taskDef.PidMode,
		PlacementConstraints:    taskDef.PlacementConstraints,
		ProxyConfiguration:      taskDef.ProxyConfiguration,
		RequiresCompatibilities: taskDef.RequiresCompatibilities,
		TaskRoleArn:             taskDef.TaskRoleArn,
		Volumes:                 taskDef.Volumes,
	}

	previousGitsha := ""
	shouldUpdate := false

	containersToUpdate := make(map[string]bool)
	for _, c := range containers {
		containersToUpdate[c] = true
	}

	// Update desired containers in task def to use same repo with new tag/sha
	for i, containerDef := range newTaskInput.ContainerDefinitions {
		// If service config does not specify which containers to update, we update all containers
		// in that task def.
		if len(containersToUpdate) != 0 {
			if found := containersToUpdate[*containerDef.Name]; !found {
				continue
			}
		}

		// Images have the form <repo-url>/<image>:<tag>
		t := strings.Split(*containerDef.Image, ":")

		if previousGitsha == "" {
			// Tag is the last element which is the SHA
			previousGitsha = t[len(t)-1]
		}

		// Only update if we find an existing image that is different from the new gitsha
		if gitsha == previousGitsha {
			continue
		}

		shouldUpdate = true

		// Get new image by using new SHA
		newImage := fmt.Sprintf("%s:%s", strings.Join(t[:len(t)-1], ""), gitsha)
		log.Printf("Changing container image %s to %s", color.Cyan(*containerDef.Image), color.Cyan(newImage))
		*newTaskInput.ContainerDefinitions[i].Image = newImage
	}

	dockerTags := newTaskInput.ContainerDefinitions[0].DockerLabels
	tags := make([]string, 0, len(dockerTags))
	for tag, value := range dockerTags {
		newTag := fmt.Sprintf("%s:%s", tag, value)
		tags = append(tags, newTag)
	}

	if !shouldUpdate {
		return updateTaskDefResult{
			// This might still be different if UpdateStrategyLatest was used
			newTaskDefARN:  *taskDef.TaskDefinitionArn,
			previousGitsha: previousGitsha,
			dockerTags:     tags,
		}, nil
	}

	// Create new task def so we can update service to use it
	respRegisterTaskDef, err := ecsClient.RegisterTaskDefinition(ctx, &newTaskInput)
	if err != nil {
		return updateTaskDefResult{}, errors.Wrapf(err, "cannot register new task definition for %s", *newTaskInput.Family)
	}

	newTaskDefArn := *respRegisterTaskDef.TaskDefinition.TaskDefinitionArn
	log.Printf("Registered new task definition %s\n", color.Cyan(newTaskDefArn))

	return updateTaskDefResult{
		newTaskDefARN:  newTaskDefArn,
		previousGitsha: previousGitsha,
		dockerTags:     tags,
	}, nil
}

// writeFailure writes the ECS failure f to the string builder.
func writeFailure(sb *strings.Builder, f ecstypes.Failure) {
	if f.Reason != nil {
		sb.WriteString(*f.Reason)
		sb.WriteString(": ")
	}
	if f.Detail != nil {
		sb.WriteString(*f.Detail)
	} else {
		sb.WriteString("unknown failure")
	}
}

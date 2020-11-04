package awsecs

import (
	"fmt"
	"log"
	"strings"

	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/goutils/color"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
	"github.com/pkg/errors"
)

// Deploy registers a new task for the given service in ECS in order to create a new deployment.
func Deploy(service *config.Service, ecsClient ecsiface.ECSAPI) error {
	// Ensure we've been passed a valid cluster ARN and exit if not
	clusterArn, err := arn.Parse(service.Cluster)
	if err != nil {
		return errors.Wrapf(err, "invalid cluster ARN: %s", service.Cluster)
	}
	log.Printf("Using cluster: %s\n", clusterArn)

	// Retrieve existing service config
	serviceInput := &ecs.DescribeServicesInput{
		Services: []*string{
			&service.Name,
		},
		Cluster: &service.Cluster,
	}

	log.Printf("Checking for service: %s\n", color.Cyan(service.Name))
	respDescribeServices, err := ecsClient.DescribeServices(serviceInput)
	if err != nil {
		return errors.Wrapf(err, "failed to find service: %s", service.Name)
	}

	taskDefARN := *respDescribeServices.Services[0].TaskDefinition
	log.Printf("Found current task definition: %v\n", taskDefARN)

	updateTaskDefRes, err := updateTaskDef(taskDefARN, service.Gitsha, ecsClient)
	if err != nil {
		return errors.Wrapf(err, "failed to update task def for service: %s", service.Name)
	}

	log.Printf(
		"Registered new task definition %s, updating service %s\n",
		color.Cyan(updateTaskDefRes.newTaskDefARN),
		color.Cyan(service.Name),
	)

	// Set dynamic service values
	// Save previous Git SHA in case we need to rollback later
	service.PreviousGitsha = updateTaskDefRes.previousGitsha
	service.PreviousTaskDefinitionARN = updateTaskDefRes.previousTaskDefARN
	service.TaskDefinitionARN = updateTaskDefRes.newTaskDefARN
	service.Tags = updateTaskDefRes.dockerTags

	err = UpdateService(service, ecsClient)
	if err != nil {
		return errors.Wrap(err, "failed to update service")
	}

	return nil
}

// UpdateService creates a new deployment on ECS.
func UpdateService(service *config.Service, ecsClient ecsiface.ECSAPI) error {
	serviceUpdateInput := &ecs.UpdateServiceInput{
		TaskDefinition:     &service.TaskDefinitionARN,
		Service:            &service.Name,
		Cluster:            &service.Cluster,
		ForceNewDeployment: aws.Bool(true),
	}

	_, err := ecsClient.UpdateService(serviceUpdateInput)
	if err != nil {
		return errors.Wrapf(err, "failed to update service %s in ECS", service.Name)
	}

	return nil
}

// CheckDrain checks if all old tasks have drained.
func CheckDrain(service *config.Service, ecsClient ecsiface.ECSAPI) (bool, error) {
	serviceInput := &ecs.DescribeServicesInput{
		Services: []*string{
			&service.Name,
		},
		Cluster: &service.Cluster,
	}

	respDescribeServices, err := ecsClient.DescribeServices(serviceInput)
	if err != nil {
		return false, errors.Wrapf(err, "failed to get current service: %s", service.Name)
	}

	for _, deployment := range respDescribeServices.Services[0].Deployments {
		if (*deployment.TaskDefinition == service.TaskDefinitionARN) && (*deployment.Status == "PRIMARY") && (*deployment.RunningCount == *deployment.DesiredCount) {
			return true, nil
		}
	}

	return false, nil
}

type updateTaskDefResult struct {
	previousTaskDefARN string
	newTaskDefARN      string
	previousGitsha     string
	dockerTags         []string
}

// updateTaskDef creates a new task def revision with the container image updated to use the new Git SHA.
// It returns the new ARN and previous Git SHA.
func updateTaskDef(taskDefARN, gitsha string, ecsClient ecsiface.ECSAPI) (updateTaskDefResult, error) {
	// Use resolved resource info to grab existing task def
	respDescribeTaskDef, err := ecsClient.DescribeTaskDefinition(&ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &taskDefARN,
	})
	if err != nil {
		return updateTaskDefResult{}, errors.Wrapf(err, "failed to get task definition: %s", taskDefARN)
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

	// Update each container in task def to use same repo with new tag/sha
	for i, container := range newTaskInput.ContainerDefinitions {
		// Images have the form <repo-url>/<image>:<tag>
		t := strings.Split(*container.Image, ":")

		if previousGitsha == "" {
			// Tag is the last element which is the SHA
			previousGitsha = t[len(t)-1]
		}

		// Get new image by using new SHA
		newImage := fmt.Sprintf("%s:%s", strings.Join(t[:len(t)-1], ""), gitsha)
		log.Printf("Changing container image %s to %s", color.Cyan(*container.Image), color.Cyan(newImage))
		*newTaskInput.ContainerDefinitions[i].Image = newImage
	}

	dockerTags := newTaskInput.ContainerDefinitions[0].DockerLabels
	tags := make([]string, 0, len(dockerTags))
	for tag, value := range dockerTags {
		newTag := fmt.Sprintf("%s:%s", tag, *value)
		tags = append(tags, newTag)
	}

	// Create new task def so we can update service to use it
	respRegisterTaskDef, err := ecsClient.RegisterTaskDefinition(&newTaskInput)
	if err != nil {
		return updateTaskDefResult{}, errors.Wrapf(err, "cannot register new task definition for %s", *newTaskInput.Family)
	}

	return updateTaskDefResult{
		previousTaskDefARN: *taskDef.TaskDefinitionArn,
		newTaskDefARN:      *respRegisterTaskDef.TaskDefinition.TaskDefinitionArn,
		previousGitsha:     previousGitsha,
		dockerTags:         tags,
	}, nil
}

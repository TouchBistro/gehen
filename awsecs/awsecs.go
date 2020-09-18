package awsecs

import (
	"fmt"
	"log"
	"strings"

	"github.com/TouchBistro/gehen/config"
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
		return errors.Wrap(err, "invalid cluster ARN: ")
	}
	log.Printf("Using cluster: %s\n", clusterArn)

	// Retrieve existing service config
	serviceInput := &ecs.DescribeServicesInput{
		Services: []*string{
			&service.Name,
		},
		Cluster: &service.Cluster,
	}

	log.Printf("Checking for service: %s\n", service.Name)
	respDescribeServices, err := ecsClient.DescribeServices(serviceInput)
	if err != nil {
		return errors.Wrap(err, "cannot get current service: ")
	}

	taskDefID := *respDescribeServices.Services[0].TaskDefinition
	log.Printf("Found current task def: %+v\n", taskDefID)

	// Use resolved service info to grab existing task def
	respDescribeTaskDef, err := ecsClient.DescribeTaskDefinition(&ecs.DescribeTaskDefinitionInput{
		TaskDefinition: &taskDefID,
	})
	if err != nil {
		return errors.Wrap(err, "cannot get task definition: ")
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
		newImage := fmt.Sprintf("%s:%s", strings.Join(t[:len(t)-1], ""), service.Gitsha)
		log.Printf("Changing container image %s to %s", *container.Image, newImage)
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
		return errors.Wrap(err, "cannot register new task definition: ")
	}

	newTaskArn := *respRegisterTaskDef.TaskDefinition.TaskDefinitionArn
	log.Printf("Registered new task definition %s, updating service %s\n", newTaskArn, service.Name)

	// Update the service to create a new deployment
	serviceUpdateInput := &ecs.UpdateServiceInput{
		TaskDefinition:     &newTaskArn,
		Service:            &service.Name,
		Cluster:            &service.Cluster,
		ForceNewDeployment: aws.Bool(true),
	}

	_, err = ecsClient.UpdateService(serviceUpdateInput)
	if err != nil {
		return errors.Wrap(err, "cannot update new task definition: ")
	}

	// Set dynamic service values
	// Save previous Git SHA in case we need to rollback later
	service.PreviousGitsha = previousGitsha
	service.PreviousTaskDefinitionARN = *taskDef.TaskDefinitionArn
	service.TaskDefinitionARN = newTaskArn
	service.Tags = tags

	return nil
}

// Rollback triggers a rollback of the service by updating the service to use the previous
// task definition.
func Rollback(service *config.Service, ecsClient ecsiface.ECSAPI) error {
	serviceUpdateInput := &ecs.UpdateServiceInput{
		TaskDefinition:     &service.PreviousTaskDefinitionARN,
		Service:            &service.Name,
		Cluster:            &service.Cluster,
		ForceNewDeployment: aws.Bool(true),
	}

	_, err := ecsClient.UpdateService(serviceUpdateInput)
	if err != nil {
		return errors.Wrap(err, "cannot update new task definition: ")
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
		return false, errors.Wrapf(err, "cannot get current service: %s", service.Name)
	}

	for _, deployment := range respDescribeServices.Services[0].Deployments {
		if (*deployment.TaskDefinition == service.TaskDefinitionARN) && (*deployment.Status == "PRIMARY") && (*deployment.RunningCount == *deployment.DesiredCount) {
			return true, nil
		}
	}

	return false, nil
}

package awsecs

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/pkg/errors"
)

const (
	TimeoutMins       = 10 // deployment check timeout in minutes
	CheckIntervalSecs = 15 // check interval in seconds
)

func Deploy(service, cluster, gitsha string, statsdClient *statsd.Client) error {
	// Ensure we've been passed a valid cluster ARN and exit if not
	clusterArn, err := arn.Parse(cluster)
	if err != nil {
		return errors.Wrap(err, "invalid cluster ARN: ")
	}
	log.Printf("Using cluster: %s\n", clusterArn)

	// Connect to ECS API
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("us-east-1"),
	}))
	svc := ecs.New(sess)

	// Retrieve existing service config
	serviceInput := &ecs.DescribeServicesInput{
		Services: []*string{
			&service,
		},
		Cluster: &cluster,
	}
	log.Printf("Checking for service: %s\n", service)
	serviceData, err := svc.DescribeServices(serviceInput)
	if err != nil {
		return errors.Wrap(err, "cannot get current service: ")
	}
	log.Printf("Found current task def: %+v\n", *serviceData.Services[0].TaskDefinition)

	// Use resolved service info to grab existing task def
	taskInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: serviceData.Services[0].TaskDefinition,
	}
	taskData, err := svc.DescribeTaskDefinition(taskInput)
	if err != nil {
		return errors.Wrap(err, "cannot get task definition: ")
	}

	// Convert API output to be ready to update task.
	newTask := taskOutToIn(*taskData)
	var dockerTags map[string]*string

	// Update each container in task def to use same repo with new tag/sha
	for i, container := range newTask.ContainerDefinitions {
		t := strings.Split(*container.Image, ":")
		newImage := fmt.Sprintf("%s:%s", strings.Join(t[:len(t)-1], ""), gitsha)
		log.Print("Changing container image " + *container.Image + " to " + newImage)
		*newTask.ContainerDefinitions[i].Image = newImage
		dockerTags = container.DockerLabels
	}

	var tags []string
	for tag, value := range dockerTags {
		newTag := tag + ":" + *value
		tags = append(tags, newTag)
	}

	taskDefReg, err := svc.RegisterTaskDefinition(&newTask)
	if err != nil {
		return errors.Wrap(err, "cannot register new task definition: ")
	}

	newTaskArn := taskDefReg.TaskDefinition.TaskDefinitionArn
	log.Printf("Registered new task definition %s, updating service %s\n", *newTaskArn, service)

	serviceUpdateInput := &ecs.UpdateServiceInput{
		TaskDefinition: newTaskArn,
		Service:        &service,
		Cluster:        &cluster,
	}

	_, err = svc.UpdateService(serviceUpdateInput)
	if err != nil {
		return errors.Wrap(err, "cannot update new task definition: ")
	}
	event := &statsd.Event{
		// Title of the event.  Required.
		Title: "Gehen Deploy Started",
		// Text is the description of the event.  Required.
		Text: "Gehen started a deploy for service " + service,
		// AggregationKey groups this event with others of the same key.
		AggregationKey: *newTaskArn,
		// Tags for the event.
		Tags: tags,
	}

	err = statsdClient.Event(event)
	if err != nil {
		return errors.Wrap(err, "cannot send statsd event")
	}
	return nil
}

func CheckDrain(service, cluster string, drained chan string, statsdClient *statsd.Client) {
	// Connect to ECS API
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("us-east-1"),
	}))
	svc := ecs.New(sess)

	serviceInput := &ecs.DescribeServicesInput{
		Services: []*string{
			&service,
		},
		Cluster: &cluster,
	}

	for {
		time.Sleep(CheckIntervalSecs * time.Second)
		log.Printf("Checking task count on: %s\n", service)
		serviceData, err := svc.DescribeServices(serviceInput)
		if err != nil {
			log.Printf("Could not get service %s\n", service)
			log.Printf("Error: %+v", err) // TODO: Remove if this is too noisy
			continue
		}
		if serviceData.Services[0].RunningCount == serviceData.Services[0].DesiredCount {
			// Use resolved service info to grab existing task def
			taskInput := &ecs.DescribeTaskDefinitionInput{
				TaskDefinition: serviceData.Services[0].TaskDefinition,
			}
			taskData, err := svc.DescribeTaskDefinition(taskInput)
			if err != nil {
				log.Printf("Could not get service %s\n", service)
				log.Printf("Error: %+v", err) // TODO: Remove if this is too noisy
				continue
			}
			dockerTags := taskData.TaskDefinition.ContainerDefinitions[0].DockerLabels
			var tags []string
			for tag, value := range dockerTags {
				newTag := tag + ":" + *value
				tags = append(tags, newTag)
			}
			event := &statsd.Event{
				// Title of the event.  Required.
				Title: "Gehen Deploy Success",
				// Text is the description of the event.  Required.
				Text: "Gehen successfully deployed " + service,
				// AggregationKey groups this event with others of the same key.
				AggregationKey: *serviceData.Services[0].TaskDefinition,
				// Tags for the event.
				Tags: tags,
			}

			err = statsdClient.Event(event)
			if err != nil {
				log.Printf("Could not get service %s\n", service)
				log.Printf("Error: %+v", err) // TODO: Remove if this is too noisy
				continue
			}
			drained <- service
		}
	}
}

func taskOutToIn(input ecs.DescribeTaskDefinitionOutput) ecs.RegisterTaskDefinitionInput {
	return ecs.RegisterTaskDefinitionInput{
		ContainerDefinitions:    input.TaskDefinition.ContainerDefinitions,
		Cpu:                     input.TaskDefinition.Cpu,
		ExecutionRoleArn:        input.TaskDefinition.ExecutionRoleArn,
		Family:                  input.TaskDefinition.Family,
		IpcMode:                 input.TaskDefinition.IpcMode,
		Memory:                  input.TaskDefinition.Memory,
		NetworkMode:             input.TaskDefinition.NetworkMode,
		PidMode:                 input.TaskDefinition.PidMode,
		PlacementConstraints:    input.TaskDefinition.PlacementConstraints,
		ProxyConfiguration:      input.TaskDefinition.ProxyConfiguration,
		RequiresCompatibilities: input.TaskDefinition.RequiresCompatibilities,
		TaskRoleArn:             input.TaskDefinition.TaskRoleArn,
		Volumes:                 input.TaskDefinition.Volumes,
	}
}

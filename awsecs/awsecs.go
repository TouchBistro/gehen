package awsecs

import (
	"log"
	"strings"

	"github.com/TouchBistro/gehen/awsecs"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/pkg/errors"
)

func Deploy(migrationCmd, service, cluster, gitsha string) error {
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

	// Update each container in task def to use same repo with new tag/sha
	for i, container := range newTask.ContainerDefinitions {
		t := strings.Split(*container.Image, ":")
		newimg := fmt.Sprintf("%s:%s", strings.Join(t[:len(t)-1], ""), gitsha)
		log.Print("Changing container image " + *container.Image + " to " + newimg)
		*newTask.ContainerDefinitions[i].Image = newimg
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

	// run migration command if one exists
	if migrationCmd != "" {
		var containerOverrides []*ecs.ContainerOverride
		var commandString []*string

		commands := strings.Split(migrationCmd, " ")
		for i := range commands {
			commandString = append(commandString, &commands[i])
		}

		containerOverrides = append(containerOverrides, &ecs.ContainerOverride{
			Name:    taskDefReg.TaskDefinition.ContainerDefinitions[0].Name,
			Command: commandString,
		})

		runTaskOverride := &ecs.TaskOverride{
			ContainerOverrides: containerOverrides,
		}

		runTaskInput := &ecs.RunTaskInput{
			TaskDefinition: newTaskArn,
			Overrides:      runTaskOverride,
			Cluster:        &cluster,
		}

		log.Printf("Launching migration for %s service with command %s\n", service, migrationCmd)
		taskRun, err := svc.RunTask(runTaskInput)
		if err != nil {
			return errors.Wrapf(err, "cannot run migration task for service %s with command %s\n", service, migrationCmd)
		}
		log.Println("Check for migration logs for " + service + " at https://app.datadoghq.com/logs?query=task_arn%3A\"" + *taskRun.Tasks[0].TaskArn + "\"")
	}

	return nil
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

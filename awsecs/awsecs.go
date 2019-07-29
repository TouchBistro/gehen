package awsecs

import (
	"log"
	"strings"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
)

func Deploy(migrationCmd, service, cluster, gitsha string) error {
	// Ensure we've been passed a valid cluster ARN and exit if not
	clusterArn, err := arn.Parse(cluster)
	if err != nil {
		return err
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
	log.Println("Checking for service: " + service)
	serviceData, err := svc.DescribeServices(serviceInput)
	if err != nil {
		return err

	}
	log.Println("Found current task def: " + *serviceData.Services[0].TaskDefinition)

	// Use resolved service info to grab existing task def
	taskInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: serviceData.Services[0].TaskDefinition,
	}
	taskData, err := svc.DescribeTaskDefinition(taskInput)
	if err != nil {
		return err
	}

	// Convert API output to be ready to update task.
	newTask := taskOutToIn(*taskData)

	// Update each container in task def to use same repo with new tag/sha
	for i, container := range newTask.ContainerDefinitions {
		t := strings.Split(*container.Image, ":")
		newimg := (strings.Join(t[:len(t)-1], "") + ":" + gitsha)
		log.Print("Changing container image " + *container.Image + " to " + newimg)
		*newTask.ContainerDefinitions[i].Image = newimg
	}

	taskDefReg, err := svc.RegisterTaskDefinition(&newTask)
	if err != nil {
		return err
	}

	newTaskArn := taskDefReg.TaskDefinition.TaskDefinitionArn
	log.Println("Registered new task definition" + *newTaskArn + ", updating service " + service)
	serviceUpdateInput := &ecs.UpdateServiceInput{
		TaskDefinition: newTaskArn,
		Service:        &service,
		Cluster:        &cluster,
	}
	_, err = svc.UpdateService(serviceUpdateInput)
	if err != nil {
		return err
	}

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
		log.Println("Launching migration for " + service + " service with command " + migrationCmd)
		taskRun, err := svc.RunTask(runTaskInput)
		if err != nil {
			return err
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

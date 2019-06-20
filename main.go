package main

import (
	"flag"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/arn"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/getsentry/raven-go"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

const timeout = 10  //deployment check timeout in minutes
const interval = 15 //check interval in seconds

func handleAwsErr(err error) {
	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ecs.ErrCodeServerException:
				log.Panic(ecs.ErrCodeServerException, aerr.Error())
			case ecs.ErrCodeClientException:
				log.Panic(ecs.ErrCodeClientException, aerr.Error())
			case ecs.ErrCodeInvalidParameterException:
				log.Panic(ecs.ErrCodeInvalidParameterException, aerr.Error())
			case ecs.ErrCodeClusterNotFoundException:
				log.Panic(ecs.ErrCodeClusterNotFoundException, aerr.Error())
			case ecs.ErrCodeServiceNotFoundException:
				log.Panic(ecs.ErrCodeServiceNotFoundException, aerr.Error())
			case ecs.ErrCodeServiceNotActiveException:
				log.Panic(ecs.ErrCodeServiceNotActiveException, aerr.Error())
			case ecs.ErrCodePlatformUnknownException:
				log.Panic(ecs.ErrCodePlatformUnknownException, aerr.Error())
			case ecs.ErrCodePlatformTaskDefinitionIncompatibilityException:
				log.Panic(ecs.ErrCodePlatformTaskDefinitionIncompatibilityException, aerr.Error())
			case ecs.ErrCodeAccessDeniedException:
				log.Panic(ecs.ErrCodeAccessDeniedException, aerr.Error())
			default:
				log.Panic(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			log.Panic(err.Error())
		}
		os.Exit(1)
	}
}

func taskOutToIn(input ecs.DescribeTaskDefinitionOutput) ecs.RegisterTaskDefinitionInput {
	output := ecs.RegisterTaskDefinitionInput{
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
	return output
}

func checkDeployment(url string, gitsha string, check chan bool) bool {
	for {
		resp, err := http.Get(url)
		if err != nil {
			raven.CaptureErrorAndWait(err, nil)
			log.Fatal(err)
		}
		b := make([]byte, 40)
		_, err = resp.Body.Read(b)
		if err != nil {
			raven.CaptureErrorAndWait(err, nil)
			log.Panic(err)
		}
		deployed := false
		respHeader := resp.Header.Get("Server")

		t := strings.Split(respHeader, "-")
		if len(t[len(t)-1]) == 40 {
			respHeader = t[len(t)-1]
		} else {
			respHeader = string(b)
		}

		if respHeader == gitsha {
			deployed = true
		}
		log.Println("Got " + respHeader + " from " + url)
		if deployed {
			check <- true
		} else {
			time.Sleep(time.Second * interval)
		}
	}
}

func init() {
	raven.SetDSN(os.Getenv("SENTRY_DSN"))
}

func main() {
	cluster := flag.String("cluster", "", "The full cluster ARN to deploy this service to")
	service := flag.String("service", "", "The service name running this service on ECS")
	gitsha := flag.String("gitsha", "", "The gitsha of the version to be deployed")
	versionUrl := flag.String("url", "", "The URL to check for the deployed version")
	flag.Parse()

	if *cluster == "" || *service == "" || *gitsha == "" || *versionUrl == "" {
		log.Fatal("Unset flags, need cluster, service, and gitsha")
	}

	//Ensure we've been passed a valid cluster ARN and panic if not
	var clusterArn, err = arn.Parse(*cluster)
	if err != nil {
		raven.CaptureErrorAndWait(err, nil)
		log.Fatal(err)
	}
	log.Println("Using cluster: " + clusterArn.String())

	//Connect to ECS API
	sess := session.Must(session.NewSession(&aws.Config{
		Region: aws.String("us-east-1"),
	}))
	svc := ecs.New(sess)

	//Retrieve existing service config
	serviceInput := &ecs.DescribeServicesInput{
		Services: []*string{
			service,
		},
		Cluster: cluster,
	}
	log.Println("Checking for service: " + *service)
	serviceData, err := svc.DescribeServices(serviceInput)
	handleAwsErr(err)

	log.Println("Found current task def: " + *serviceData.Services[0].TaskDefinition)
	//Use resolved service info to grab existing task def
	taskInput := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: serviceData.Services[0].TaskDefinition,
	}
	taskData, err := svc.DescribeTaskDefinition(taskInput)
	handleAwsErr(err)

	//Convert API output to be ready to update task (These probably map through an interface somehow but I'm bad at golang?)
	newTask := taskOutToIn(*taskData)

	//Update each container in task def to use same repo with new tag/sha
	for i, container := range newTask.ContainerDefinitions {
		log.Print("Changing container image " + *container.Image + " to ")
		t := strings.Split(*container.Image, ":")
		newimg := (strings.Join(t[:len(t)-1], "") + ":" + *gitsha)
		log.Println(newimg)
		*newTask.ContainerDefinitions[i].Image = newimg
	}

	log.Println("Registering new task definition")
	taskDefReg, err := svc.RegisterTaskDefinition(&newTask)
	handleAwsErr(err)

	newTaskArn := taskDefReg.TaskDefinition.TaskDefinitionArn
	log.Println("Updating service to new task def " + *newTaskArn) //Pick something to actually print
	serviceUpdateInput := &ecs.UpdateServiceInput{
		Service:        service,
		TaskDefinition: newTaskArn,
		Cluster:        cluster,
	}
	_, err = svc.UpdateService(serviceUpdateInput)
	handleAwsErr(err)

	log.Println("Checking " + *versionUrl + " for newly deployed version")
	check := make(chan bool)
	go checkDeployment(*versionUrl, *gitsha, check)
	select {
	case _ = <-check:
		log.Println("Version " + *gitsha + " successfully deployed to " + *service)
		return
	case <-time.After(timeout * time.Minute):
		log.Println("Timed out while checking for deployed version on " + *service)
		os.Exit(1)
	}
}

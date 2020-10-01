package awsecs

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
)

type mockService struct {
	name             string
	taskDefVersion   int
	imageName        string
	gitsha           string
	deploymentStatus string
}

func (ms *mockService) TaskDefinitionArn() string {
	return fmt.Sprintf("arn:aws:ecs:us-east-1:123456:task-definition/%s:%d", ms.name, ms.taskDefVersion)
}

type MockClient struct {
	ecsiface.ECSAPI
	services map[string]*mockService
}

func NewMockClient(serviceNames []string, imageName, gitsha string) *MockClient {
	services := make(map[string]*mockService)

	for _, s := range serviceNames {
		services[s] = &mockService{
			name:             s,
			taskDefVersion:   1,
			imageName:        imageName,
			gitsha:           gitsha,
			deploymentStatus: "PRIMARY",
		}
	}

	return &MockClient{
		services: services,
	}
}

func (mc *MockClient) SetServiceStatus(name, status string) {
	s, ok := mc.services[name]
	if !ok {
		panic(fmt.Sprintf("mock ECS service %s not found", name))
	}

	s.deploymentStatus = status
}

func (mc *MockClient) DescribeServices(input *ecs.DescribeServicesInput) (*ecs.DescribeServicesOutput, error) {
	outServices := make([]*ecs.Service, 0)
	for _, serviceName := range input.Services {
		s, ok := mc.services[*serviceName]
		if !ok {
			return nil, errors.New("Service not found")
		}

		outServices = append(outServices, &ecs.Service{
			ServiceName:    aws.String(s.name),
			TaskDefinition: aws.String(s.TaskDefinitionArn()),
			Deployments: []*ecs.Deployment{
				{
					TaskDefinition: aws.String(s.TaskDefinitionArn()),
					Status:         aws.String(s.deploymentStatus),
					// TODO should this be configurable?
					RunningCount: aws.Int64(2),
					DesiredCount: aws.Int64(2),
				},
			},
		})
	}

	return &ecs.DescribeServicesOutput{
		Services: outServices,
	}, nil
}

func (mc *MockClient) DescribeTaskDefinition(input *ecs.DescribeTaskDefinitionInput) (*ecs.DescribeTaskDefinitionOutput, error) {
	var service *mockService
	for _, s := range mc.services {
		if s.TaskDefinitionArn() == *input.TaskDefinition {
			service = s
		}
	}

	if service == nil {
		return nil, errors.New("Task Definition not found")
	}

	image := fmt.Sprintf("123456.dkr.ecr.us-east-1.amazonaws.com/%s:%s", service.imageName, service.gitsha)
	return &ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &ecs.TaskDefinition{
			ContainerDefinitions: []*ecs.ContainerDefinition{
				{
					Image: aws.String(image),
				},
			},
			// This is the actual task def name
			Family:            aws.String(service.name),
			TaskDefinitionArn: aws.String(service.TaskDefinitionArn()),
		},
	}, nil
}

func (mc *MockClient) RegisterTaskDefinition(input *ecs.RegisterTaskDefinitionInput) (*ecs.RegisterTaskDefinitionOutput, error) {
	service, ok := mc.services[*input.Family]
	if !ok {
		return nil, errors.New("Task definition family not found")
	}

	// "Create" new task def version
	service.taskDefVersion++

	return &ecs.RegisterTaskDefinitionOutput{
		TaskDefinition: &ecs.TaskDefinition{
			TaskDefinitionArn: aws.String(service.TaskDefinitionArn()),
		},
	}, nil
}

func (mc *MockClient) UpdateService(input *ecs.UpdateServiceInput) (*ecs.UpdateServiceOutput, error) {
	_, ok := mc.services[*input.Service]
	if !ok {
		return nil, errors.New("Service not found")
	}

	// We don't actually use the return value
	return &ecs.UpdateServiceOutput{}, nil
}

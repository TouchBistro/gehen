package awsecs

import (
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/aws/aws-sdk-go/service/ecs/ecsiface"
	"github.com/aws/aws-sdk-go/service/eventbridge"
	"github.com/aws/aws-sdk-go/service/eventbridge/eventbridgeiface"
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

type MockECSClient struct {
	ecsiface.ECSAPI
	services map[string]*mockService
}

func NewMockECSClient(serviceNames []string, imageName, gitsha string) *MockECSClient {
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

	return &MockECSClient{
		services: services,
	}
}

func (mc *MockECSClient) SetServiceStatus(name, status string) {
	s, ok := mc.services[name]
	if !ok {
		panic(fmt.Sprintf("mock ECS service %s not found", name))
	}

	s.deploymentStatus = status
}

func (mc *MockECSClient) DescribeServices(input *ecs.DescribeServicesInput) (*ecs.DescribeServicesOutput, error) {
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

func (mc *MockECSClient) DescribeTaskDefinition(input *ecs.DescribeTaskDefinitionInput) (*ecs.DescribeTaskDefinitionOutput, error) {
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

func (mc *MockECSClient) RegisterTaskDefinition(input *ecs.RegisterTaskDefinitionInput) (*ecs.RegisterTaskDefinitionOutput, error) {
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

func (mc *MockECSClient) UpdateService(input *ecs.UpdateServiceInput) (*ecs.UpdateServiceOutput, error) {
	_, ok := mc.services[*input.Service]
	if !ok {
		return nil, errors.New("Service not found")
	}

	// We don't actually use the return value
	return &ecs.UpdateServiceOutput{}, nil
}

// Event Bridge mocks

type mockScheduledTask struct {
	name       string
	taskDefARN string
}

type MockEventBridgeClient struct {
	eventbridgeiface.EventBridgeAPI
	tasks map[string]*mockScheduledTask
}

func NewMockEventBridgeClient(taskNames []string) *MockEventBridgeClient {
	tasks := make(map[string]*mockScheduledTask)

	for _, t := range taskNames {
		tasks[t] = &mockScheduledTask{
			name:       t,
			taskDefARN: fmt.Sprintf("arn:aws:ecs:us-east-1:123456:task-definition/%s:1", t),
		}
	}

	return &MockEventBridgeClient{
		tasks: tasks,
	}
}

func (mc *MockEventBridgeClient) ListTargetsByRule(input *eventbridge.ListTargetsByRuleInput) (*eventbridge.ListTargetsByRuleOutput, error) {
	t, ok := mc.tasks[*input.Rule]
	if !ok {
		return nil, errors.New("Rule not found")
	}

	return &eventbridge.ListTargetsByRuleOutput{
		Targets: []*eventbridge.Target{
			{
				EcsParameters: &eventbridge.EcsParameters{
					TaskDefinitionArn: aws.String(t.taskDefARN),
				},
			},
		},
	}, nil
}

func (mc *MockEventBridgeClient) PutTargets(input *eventbridge.PutTargetsInput) (*eventbridge.PutTargetsOutput, error) {
	t, ok := mc.tasks[*input.Rule]
	if !ok {
		return nil, errors.New("Rule not found")
	}

	t.taskDefARN = *input.Targets[0].EcsParameters.TaskDefinitionArn
	return &eventbridge.PutTargetsOutput{
		FailedEntryCount: aws.Int64(0),
	}, nil
}

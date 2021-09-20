package awsecs

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strconv"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	ebtypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
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

type mockTask struct {
	id          string
	taskDefArn  string
	healthy     bool
	serviceName string
	clusterName string
}

func (mt *mockTask) Arn() string {
	return fmt.Sprintf("arn:aws:ecs:us-east-1:123456:task/%s/%s", mt.clusterName, mt.id)
}

func (mt *mockTask) HealthStatus() ecstypes.HealthStatus {
	if mt.healthy {
		return ecstypes.HealthStatusHealthy
	}
	return ecstypes.HealthStatusUnhealthy
}

type MockECSClient struct {
	services map[string]*mockService
	tasks    []mockTask
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

func (mc *MockECSClient) DescribeServices(ctx context.Context, params *ecs.DescribeServicesInput, optFns ...func(*ecs.Options)) (*ecs.DescribeServicesOutput, error) {
	var outServices []ecstypes.Service
	for _, serviceName := range params.Services {
		s, ok := mc.services[serviceName]
		if !ok {
			return nil, errors.New("service not found")
		}
		outServices = append(outServices, ecstypes.Service{
			ServiceName:    aws.String(s.name),
			TaskDefinition: aws.String(s.TaskDefinitionArn()),
			Deployments: []ecstypes.Deployment{
				{
					TaskDefinition: aws.String(s.TaskDefinitionArn()),
					Status:         aws.String(s.deploymentStatus),
					// TODO should this be configurable?
					RunningCount: 2,
					DesiredCount: 2,
				},
			},
		})
	}
	return &ecs.DescribeServicesOutput{Services: outServices}, nil
}

func (mc *MockECSClient) DescribeTaskDefinition(ctx context.Context, params *ecs.DescribeTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTaskDefinitionOutput, error) {
	var service *mockService
	for _, s := range mc.services {
		if s.TaskDefinitionArn() == *params.TaskDefinition {
			service = s
		}
	}

	if service == nil {
		return nil, errors.New("task Definition not found")
	}

	image := fmt.Sprintf("123456.dkr.ecr.us-east-1.amazonaws.com/%s:%s", service.imageName, service.gitsha)
	return &ecs.DescribeTaskDefinitionOutput{
		TaskDefinition: &ecstypes.TaskDefinition{
			ContainerDefinitions: []ecstypes.ContainerDefinition{
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

func (mc *MockECSClient) RegisterTaskDefinition(ctx context.Context, params *ecs.RegisterTaskDefinitionInput, optFns ...func(*ecs.Options)) (*ecs.RegisterTaskDefinitionOutput, error) {
	service, ok := mc.services[*params.Family]
	if !ok {
		return nil, errors.New("task definition family not found")
	}

	// "Create" new task def version
	service.taskDefVersion++
	return &ecs.RegisterTaskDefinitionOutput{
		TaskDefinition: &ecstypes.TaskDefinition{
			TaskDefinitionArn: aws.String(service.TaskDefinitionArn()),
		},
	}, nil
}

func (mc *MockECSClient) UpdateService(ctx context.Context, params *ecs.UpdateServiceInput, optFns ...func(*ecs.Options)) (*ecs.UpdateServiceOutput, error) {
	_, ok := mc.services[*params.Service]
	if !ok {
		return nil, errors.New("service not found")
	}

	// We don't actually use the return value
	return &ecs.UpdateServiceOutput{}, nil
}

func (mc *MockECSClient) CreateMockTasks(clusterName, serviceName, taskDefArn string, healthy bool, count int) {
	for i := 0; i < count; i++ {
		mc.tasks = append(mc.tasks, mockTask{
			id:          strconv.Itoa(rand.Int()),
			clusterName: clusterName,
			serviceName: serviceName,
			taskDefArn:  taskDefArn,
			healthy:     healthy,
		})
	}
}

func (mc *MockECSClient) ListTasks(ctx context.Context, params *ecs.ListTasksInput, optFns ...func(*ecs.Options)) (*ecs.ListTasksOutput, error) {
	var taskArns []string
	for _, t := range mc.tasks {
		if params.Cluster != nil && t.clusterName != *params.Cluster {
			continue
		}
		if params.ServiceName != nil && t.serviceName != *params.ServiceName {
			continue
		}
		taskArns = append(taskArns, t.Arn())
	}
	return &ecs.ListTasksOutput{TaskArns: taskArns}, nil
}

func (mc *MockECSClient) DescribeTasks(ctx context.Context, params *ecs.DescribeTasksInput, optFns ...func(*ecs.Options)) (*ecs.DescribeTasksOutput, error) {
	arnSet := make(map[string]bool)
	for _, arn := range params.Tasks {
		arnSet[arn] = true
	}

	var tasks []ecstypes.Task
	for _, t := range mc.tasks {
		if params.Cluster != nil && t.clusterName != *params.Cluster {
			continue
		}
		ok := arnSet[t.Arn()]
		if params.Tasks != nil && !ok {
			continue
		}
		tasks = append(tasks, ecstypes.Task{
			TaskArn:           aws.String(t.Arn()),
			TaskDefinitionArn: aws.String(t.taskDefArn),
			HealthStatus:      t.HealthStatus(),
		})
	}
	return &ecs.DescribeTasksOutput{Tasks: tasks}, nil
}

// Event Bridge mocks

type mockScheduledTask struct {
	name       string
	taskDefARN string
}

type MockEventBridgeClient struct {
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

func (mc *MockEventBridgeClient) ListTargetsByRule(ctx context.Context, params *eventbridge.ListTargetsByRuleInput, optFns ...func(*eventbridge.Options)) (*eventbridge.ListTargetsByRuleOutput, error) {
	t, ok := mc.tasks[*params.Rule]
	if !ok {
		return nil, errors.New("rule not found")
	}

	return &eventbridge.ListTargetsByRuleOutput{
		Targets: []ebtypes.Target{
			{
				EcsParameters: &ebtypes.EcsParameters{
					TaskDefinitionArn: aws.String(t.taskDefARN),
				},
			},
		},
	}, nil
}

func (mc *MockEventBridgeClient) PutTargets(ctx context.Context, params *eventbridge.PutTargetsInput, optFns ...func(*eventbridge.Options)) (*eventbridge.PutTargetsOutput, error) {
	t, ok := mc.tasks[*params.Rule]
	if !ok {
		return nil, errors.New("rule not found")
	}
	t.taskDefARN = *params.Targets[0].EcsParameters.TaskDefinitionArn
	return &eventbridge.PutTargetsOutput{FailedEntryCount: 0}, nil
}

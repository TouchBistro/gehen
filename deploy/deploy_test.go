package deploy_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TouchBistro/gehen/awsecs"
	"github.com/TouchBistro/gehen/config"
	"github.com/TouchBistro/gehen/deploy"
	"github.com/stretchr/testify/assert"
)

func TestDeploy(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	previousGitsha := "b6589fc6ab0dc82cf12099d1c2d40ab994e8410c"
	services := []*config.Service{
		{
			Name:    "example-production",
			Gitsha:  gitsha,
			Cluster: "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
			URL:     "https://example.touchbistro.io/ping",
		},
		{
			Name:    "example-staging",
			Gitsha:  gitsha,
			Cluster: "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
			URL:     "https://staging.example.touchbistro.io/ping",
		},
	}

	mockClient := awsecs.NewMockECSClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		previousGitsha,
	)

	expectedResults := []deploy.Result{
		{
			Service: &config.Service{
				Name:                      "example-production",
				Gitsha:                    gitsha,
				Cluster:                   "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
				URL:                       "https://example.touchbistro.io/ping",
				PreviousGitsha:            previousGitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/example-production:2",
				Tags:                      []string{},
			},
			Err: nil,
		},
		{
			Service: &config.Service{
				Name:                      "example-staging",
				Gitsha:                    gitsha,
				Cluster:                   "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
				URL:                       "https://staging.example.touchbistro.io/ping",
				PreviousGitsha:            previousGitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:2",
				Tags:                      []string{},
			},
			Err: nil,
		},
	}

	results := deploy.Deploy(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestRollback(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	previousGitsha := "b6589fc6ab0dc82cf12099d1c2d40ab994e8410c"
	services := []*config.Service{
		{
			Name:                      "example-production",
			Gitsha:                    gitsha,
			Cluster:                   "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
			URL:                       "https://example.touchbistro.io/ping",
			PreviousGitsha:            previousGitsha,
			PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
			TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/example-production:2",
			Tags:                      []string{},
		},
		{
			Name:                      "example-staging",
			Gitsha:                    gitsha,
			Cluster:                   "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
			URL:                       "https://staging.example.touchbistro.io/ping",
			PreviousGitsha:            previousGitsha,
			PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
			TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:2",
			Tags:                      []string{},
		},
	}

	mockClient := awsecs.NewMockECSClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		previousGitsha,
	)

	expectedResults := []deploy.Result{
		{
			Service: &config.Service{
				Name:                      "example-production",
				Gitsha:                    previousGitsha,
				Cluster:                   "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
				URL:                       "https://example.touchbistro.io/ping",
				PreviousGitsha:            gitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:2",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
				Tags:                      []string{},
			},
			Err: nil,
		},
		{
			Service: &config.Service{
				Name:                      "example-staging",
				Gitsha:                    previousGitsha,
				Cluster:                   "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
				URL:                       "https://staging.example.touchbistro.io/ping",
				PreviousGitsha:            gitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:2",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
				Tags:                      []string{},
			},
			Err: nil,
		},
	}

	results := deploy.Rollback(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDeployed(t *testing.T) {
	deploy.TimeoutDuration(3 * time.Second)
	deploy.CheckIntervalDuration(250 * time.Millisecond)

	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"

	prodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := fmt.Sprintf("example-service:api-%s", gitsha)
		w.Header().Add("Server", v)
		fmt.Fprint(w, "OK")
	}))
	defer prodServer.Close()

	stagingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := fmt.Sprintf("example-service:api-%s", gitsha)
		w.Header().Add("Server", v)
		fmt.Fprint(w, "OK")
	}))
	defer stagingServer.Close()

	services := []*config.Service{
		{
			Name:   "example-production",
			Gitsha: gitsha,
			URL:    prodServer.URL,
		},
		{
			Name:   "example-staging",
			Gitsha: gitsha,
			URL:    stagingServer.URL,
		},
	}

	expectedResults := []deploy.Result{
		{
			Service: &config.Service{
				Name:   "example-production",
				Gitsha: gitsha,
				URL:    prodServer.URL,
			},
			Err: nil,
		},
		{
			Service: &config.Service{
				Name:   "example-staging",
				Gitsha: gitsha,
				URL:    stagingServer.URL,
			},
			Err: nil,
		},
	}

	results := deploy.CheckDeployed(services)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDeployFailed(t *testing.T) {
	deploy.TimeoutDuration(1 * time.Second)
	deploy.CheckIntervalDuration(250 * time.Millisecond)

	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	previousGitsha := "b6589fc6ab0dc82cf12099d1c2d40ab994e8410c"

	prodServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := fmt.Sprintf("example-service:api-%s", previousGitsha)
		w.Header().Add("Server", v)
		fmt.Fprint(w, "OK")
	}))
	defer prodServer.Close()

	stagingServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v := fmt.Sprintf("example-service:api-%s", previousGitsha)
		w.Header().Add("Server", v)
		fmt.Fprint(w, "OK")
	}))
	defer stagingServer.Close()

	services := []*config.Service{
		{
			Name:   "example-production",
			Gitsha: gitsha,
			URL:    prodServer.URL,
		},
		{
			Name:   "example-staging",
			Gitsha: gitsha,
			URL:    stagingServer.URL,
		},
	}

	expectedResults := []deploy.Result{
		{
			Service: &config.Service{
				Name:   "example-production",
				Gitsha: gitsha,
				URL:    prodServer.URL,
			},
			Err: deploy.ErrTimedOut,
		},
		{
			Service: &config.Service{
				Name:   "example-staging",
				Gitsha: gitsha,
				URL:    stagingServer.URL,
			},
			Err: deploy.ErrTimedOut,
		},
	}

	results := deploy.CheckDeployed(services)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDrain(t *testing.T) {
	deploy.TimeoutDuration(3 * time.Second)
	deploy.CheckIntervalDuration(250 * time.Millisecond)

	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	services := []*config.Service{
		{
			Name:              "example-production",
			Gitsha:            gitsha,
			Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
			URL:               "https://example.touchbistro.io/ping",
			TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
		},
		{
			Name:              "example-staging",
			Gitsha:            gitsha,
			Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
			URL:               "https://staging.example.touchbistro.io/ping",
			TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
		},
	}

	mockClient := awsecs.NewMockECSClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		gitsha,
	)

	expectedResults := []deploy.Result{
		{
			Service: &config.Service{
				Name:              "example-production",
				Gitsha:            gitsha,
				Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
				URL:               "https://example.touchbistro.io/ping",
				TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
			},
			Err: nil,
		},
		{
			Service: &config.Service{
				Name:              "example-staging",
				Gitsha:            gitsha,
				Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
				URL:               "https://staging.example.touchbistro.io/ping",
				TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
			},
			Err: nil,
		},
	}

	results := deploy.CheckDrained(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDrainFailed(t *testing.T) {
	deploy.TimeoutDuration(1 * time.Second)
	deploy.CheckIntervalDuration(250 * time.Millisecond)

	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	services := []*config.Service{
		{
			Name:              "example-production",
			Gitsha:            gitsha,
			Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
			URL:               "https://example.touchbistro.io/ping",
			TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
		},
		{
			Name:              "example-staging",
			Gitsha:            gitsha,
			Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
			URL:               "https://staging.example.touchbistro.io/ping",
			TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
		},
	}

	mockClient := awsecs.NewMockECSClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		gitsha,
	)

	mockClient.SetServiceStatus("example-production", "ACTIVE")

	expectedResults := []deploy.Result{
		{
			Service: &config.Service{
				Name:              "example-production",
				Gitsha:            gitsha,
				Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
				URL:               "https://example.touchbistro.io/ping",
				TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
			},
			Err: deploy.ErrTimedOut,
		},
		{
			Service: &config.Service{
				Name:              "example-staging",
				Gitsha:            gitsha,
				Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
				URL:               "https://staging.example.touchbistro.io/ping",
				TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-staging:1",
			},
			Err: nil,
		},
	}

	results := deploy.CheckDrained(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestUpdateScheduledTasks(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	previousGitsha := "b6589fc6ab0dc82cf12099d1c2d40ab994e8410c"
	scheduledTasks := []*config.ScheduledTask{
		{
			Name:   "weekly-job",
			Gitsha: gitsha,
		},
		{
			Name:   "monthly-job",
			Gitsha: gitsha,
		},
	}

	mockECSClient := awsecs.NewMockECSClient(
		[]string{
			"weekly-job",
			"monthly-job",
		},
		"example-service",
		previousGitsha,
	)

	mockEBClient := awsecs.NewMockEventBridgeClient([]string{
		"weekly-job",
		"monthly-job",
	})

	expectedResults := []deploy.ScheduledTaskResult{
		{
			Task: &config.ScheduledTask{
				Name:                      "weekly-job",
				Gitsha:                    gitsha,
				PreviousGitsha:            previousGitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/weekly-job:1",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/weekly-job:2",
			},
			Err: nil,
		},
		{
			Task: &config.ScheduledTask{
				Name:                      "monthly-job",
				Gitsha:                    gitsha,
				PreviousGitsha:            previousGitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/monthly-job:1",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/monthly-job:2",
			},
			Err: nil,
		},
	}

	results := deploy.UpdateScheduledTasks(scheduledTasks, mockEBClient, mockECSClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestRollbackScheduledTasks(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	previousGitsha := "b6589fc6ab0dc82cf12099d1c2d40ab994e8410c"
	scheduledTasks := []*config.ScheduledTask{
		{
			Name:                      "weekly-job",
			Gitsha:                    gitsha,
			PreviousGitsha:            previousGitsha,
			PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/weekly-job:1",
			TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/weekly-job:2",
		},
		{
			Name:                      "monthly-job",
			Gitsha:                    gitsha,
			PreviousGitsha:            previousGitsha,
			PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/monthly-job:1",
			TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/monthly-job:2",
		},
	}

	mockECSClient := awsecs.NewMockECSClient(
		[]string{
			"weekly-job",
			"monthly-job",
		},
		"example-service",
		previousGitsha,
	)

	mockEBClient := awsecs.NewMockEventBridgeClient([]string{
		"weekly-job",
		"monthly-job",
	})

	expectedResults := []deploy.ScheduledTaskResult{
		{
			Task: &config.ScheduledTask{
				Name:                      "weekly-job",
				Gitsha:                    gitsha,
				PreviousGitsha:            previousGitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/weekly-job:1",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/weekly-job:2",
			},
			Err: nil,
		},
		{
			Task: &config.ScheduledTask{
				Name:                      "monthly-job",
				Gitsha:                    gitsha,
				PreviousGitsha:            previousGitsha,
				PreviousTaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/monthly-job:1",
				TaskDefinitionARN:         "arn:aws:ecs:us-east-1:123456:task-definition/monthly-job:2",
			},
			Err: nil,
		},
	}

	results := deploy.RollbackScheduledTasks(scheduledTasks, mockEBClient, mockECSClient)

	assert.ElementsMatch(t, expectedResults, results)
}

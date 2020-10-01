package deploy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/TouchBistro/gehen/awsecs"
	"github.com/TouchBistro/gehen/config"
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

	mockClient := awsecs.NewMockClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		previousGitsha,
	)

	expectedResults := []Result{
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

	results := Deploy(services, mockClient)

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

	mockClient := awsecs.NewMockClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		previousGitsha,
	)

	expectedResults := []Result{
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

	results := Rollback(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDeployed(t *testing.T) {
	timeoutDuration = 3 * time.Second
	checkIntervalDuration = 250 * time.Millisecond

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

	expectedResults := []Result{
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

	results := CheckDeployed(services)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDeployFailed(t *testing.T) {
	timeoutDuration = 1 * time.Second
	checkIntervalDuration = 250 * time.Millisecond

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

	expectedResults := []Result{
		{
			Service: &config.Service{
				Name:   "example-production",
				Gitsha: gitsha,
				URL:    prodServer.URL,
			},
			Err: ErrTimedOut,
		},
		{
			Service: &config.Service{
				Name:   "example-staging",
				Gitsha: gitsha,
				URL:    stagingServer.URL,
			},
			Err: ErrTimedOut,
		},
	}

	results := CheckDeployed(services)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDrain(t *testing.T) {
	timeoutDuration = 3 * time.Second
	checkIntervalDuration = 250 * time.Millisecond

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

	mockClient := awsecs.NewMockClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		gitsha,
	)

	expectedResults := []Result{
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

	results := CheckDrained(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

func TestCheckDrainFailed(t *testing.T) {
	timeoutDuration = 1 * time.Second
	checkIntervalDuration = 250 * time.Millisecond

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

	mockClient := awsecs.NewMockClient(
		[]string{
			"example-production",
			"example-staging",
		},
		"example-service",
		gitsha,
	)

	mockClient.SetServiceStatus("example-production", "ACTIVE")

	expectedResults := []Result{
		{
			Service: &config.Service{
				Name:              "example-production",
				Gitsha:            gitsha,
				Cluster:           "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
				URL:               "https://example.touchbistro.io/ping",
				TaskDefinitionARN: "arn:aws:ecs:us-east-1:123456:task-definition/example-production:1",
			},
			Err: ErrTimedOut,
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

	results := CheckDrained(services, mockClient)

	assert.ElementsMatch(t, expectedResults, results)
}

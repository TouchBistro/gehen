package config_test

import (
	"testing"

	"github.com/TouchBistro/gehen/config"
	"github.com/stretchr/testify/assert"
)

func TestReadServicesWithRole(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	expectedServices := []*config.Service{
		{
			Name:           "example-production",
			Gitsha:         gitsha,
			Cluster:        "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
			URL:            "https://example.touchbistro.io/ping",
			UpdateStrategy: config.UpdateStrategyLatest,
			Containers:     []string{"sidecar", "service"},
		},
		{
			Name:           "example-staging",
			Gitsha:         gitsha,
			Cluster:        "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
			URL:            "https://staging.example.touchbistro.io/ping",
			UpdateStrategy: config.UpdateStrategyLatest,
		},
	}
	expectedScheduledTasks := []*config.ScheduledTask{
		{
			Name:           "weekly-job",
			Gitsha:         gitsha,
			UpdateStrategy: config.UpdateStrategyLatest,
		},
		{
			Name:           "monthly-job",
			Gitsha:         gitsha,
			UpdateStrategy: config.UpdateStrategyLatest,
		},
	}

	expectedRole := &config.Role{
		ARN: "arn:aws:iam::123456:role/OrganizationAccountAccessRole",
	}

	parsedConfig, err := config.Read("testdata/gehen.good.yml", gitsha)

	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedServices, parsedConfig.Services)
	assert.ElementsMatch(t, expectedScheduledTasks, parsedConfig.ScheduledTasks)
	assert.Equal(t, expectedRole, parsedConfig.Role)
	assert.Equal(t, 5, parsedConfig.TimeoutMinutes)
}

func TestReadServicesWithoutRole(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	expectedServices := []*config.Service{
		{
			Name:           "example-production",
			Gitsha:         gitsha,
			Cluster:        "arn:aws:ecs:us-east-1:123456:cluster/prod-cluster",
			URL:            "https://example.touchbistro.io/ping",
			UpdateStrategy: config.UpdateStrategyCurrent,
		},
		{
			Name:           "example-staging",
			Gitsha:         gitsha,
			Cluster:        "arn:aws:ecs:us-east-1:123456:cluster/non-prod-cluster",
			URL:            "https://staging.example.touchbistro.io/ping",
			UpdateStrategy: config.UpdateStrategyCurrent,
		},
	}
	expectedScheduledTasks := []*config.ScheduledTask{
		{
			Name:           "weekly-job",
			Gitsha:         gitsha,
			UpdateStrategy: config.UpdateStrategyCurrent,
		},
		{
			Name:           "monthly-job",
			Gitsha:         gitsha,
			UpdateStrategy: config.UpdateStrategyCurrent,
		},
	}

	parsedConfig, err := config.Read("testdata/gehen.no-role.yml", gitsha)

	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedServices, parsedConfig.Services)
	assert.ElementsMatch(t, expectedScheduledTasks, parsedConfig.ScheduledTasks)
	assert.Nil(t, parsedConfig.Role)
	assert.Equal(t, 0, parsedConfig.TimeoutMinutes)
}

func TestReadServicesInvalid(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	parsedConfig, err := config.Read("testdata/gehen.bad.yml", gitsha)

	assert.Error(t, err)
	assert.Nil(t, parsedConfig.Services)
	assert.Nil(t, parsedConfig.ScheduledTasks)
	assert.Nil(t, parsedConfig.Role)
	assert.Equal(t, 0, parsedConfig.TimeoutMinutes)
}

func TestNoGehenYaml(t *testing.T) {
	parsedConfig, err := config.Read("testdata/gehen.notfound.yml", "")

	assert.Error(t, err)
	assert.Nil(t, parsedConfig.Services)
	assert.Nil(t, parsedConfig.ScheduledTasks)
	assert.Nil(t, parsedConfig.Role)
	assert.Equal(t, 0, parsedConfig.TimeoutMinutes)
}

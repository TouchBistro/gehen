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
			Name:   "weekly-job",
			Gitsha: gitsha,
		},
		{
			Name:   "monthly-job",
			Gitsha: gitsha,
		},
	}

	expectedRole := &config.Role{
		ARN: "arn:aws:iam::123456:role/OrganizationAccountAccessRole",
	}

	services, scheduledTasks, role, err := config.Read("testdata/gehen.good.yml", gitsha)

	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedServices, services)
	assert.ElementsMatch(t, expectedScheduledTasks, scheduledTasks)
	assert.Equal(t, expectedRole, role)
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
			Name:   "weekly-job",
			Gitsha: gitsha,
		},
		{
			Name:   "monthly-job",
			Gitsha: gitsha,
		},
	}

	services, scheduledTasks, role, err := config.Read("testdata/gehen.no-role.yml", gitsha)

	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedServices, services)
	assert.ElementsMatch(t, expectedScheduledTasks, scheduledTasks)
	assert.Nil(t, role)
}

func TestReadServicesInvalid(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	services, scheduledTasks, role, err := config.Read("testdata/gehen.bad.yml", gitsha)

	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Nil(t, scheduledTasks)
	assert.Nil(t, role)
}

func TestNoGehenYaml(t *testing.T) {
	services, scheduledTasks, role, err := config.Read("testdata/gehen.notfound.yml", "")

	assert.Error(t, err)
	assert.Nil(t, services)
	assert.Nil(t, scheduledTasks)
	assert.Nil(t, role)
}

package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestReadServices(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	expectedServices := []Service{
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

	services, err := ReadServices("testdata/gehen.good.yml", gitsha)

	assert.NoError(t, err)
	assert.ElementsMatch(t, expectedServices, services)
}

func TestReadServicesInvalid(t *testing.T) {
	gitsha := "da39a3ee5e6b4b0d3255bfef95601890afd80709"
	services, err := ReadServices("testdata/gehen.bad.yml", gitsha)

	assert.Error(t, err)
	assert.Nil(t, services)
}

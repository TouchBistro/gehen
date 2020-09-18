package config

import (
	"os"

	"github.com/TouchBistro/goutils/file"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

type serviceConfig struct {
	Cluster string `yaml:"cluster"`
	URL     string `yaml:"url"`
}

type gehenConfig struct {
	Services map[string]serviceConfig `yaml:"services"`
}

// Service represents a service that can be deployed by gehen.
type Service struct {
	Name    string
	Gitsha  string
	Cluster string
	URL     string
	// The Git SHA of the previous deployment. Used by Gehen for rollback purposes.
	// Please do not modify this value.
	PreviousGitsha            string
	PreviousTaskDefinitionARN string
	TaskDefinitionARN         string
	Tags                      []string
}

// ReadServices reads the config file at the given path and returns
// a slice of services.
func ReadServices(configPath, gitsha string) ([]Service, error) {
	if !file.FileOrDirExists(configPath) {
		return nil, errors.Errorf("No such file %s", configPath)
	}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, errors.Wrapf(err, "failed to open file %s", configPath)
	}
	defer file.Close()

	var config gehenConfig
	err = yaml.NewDecoder(file).Decode(&config)
	if err != nil {
		return nil, errors.Wrapf(err, "couldn't read yaml file at %s", configPath)
	}

	services := make([]Service, 0, len(config.Services))
	for name, s := range config.Services {
		service := Service{
			Name:    name,
			Gitsha:  gitsha,
			Cluster: s.Cluster,
			URL:     s.URL,
		}
		services = append(services, service)
	}

	return services, nil
}

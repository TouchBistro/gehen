package config

import (
	"os"
	"strings"

	"github.com/TouchBistro/goutils/file"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

const (
	UpdateStrategyCurrent = "current"
	UpdateStrategyLatest  = "latest"
	UpdateStrategyNone    = "none"
)

type serviceConfig struct {
	Cluster    string   `yaml:"cluster"`
	URL        string   `yaml:"url"`
	Containers []string `yaml:"containers"`
}

type scheduledTaskConfig struct{}

type gehenConfig struct {
	Services       map[string]serviceConfig       `yaml:"services"`
	ScheduledTasks map[string]scheduledTaskConfig `yaml:"scheduledTasks"`
	Role           Role                           `yaml:"role"`
	TimeoutMinutes int                            `yaml:"timeoutMinutes"`
	UpdateStrategy string                         `yaml:"updateStrategy"`
}

// Role represents an IAM role to assume
type Role struct {
	ARN string `yaml:"arn"`
}

// Service represents a service that can be deployed by gehen.
type Service struct {
	Name           string
	Gitsha         string
	Cluster        string
	URL            string
	UpdateStrategy string
	Containers     []string
	// The Git SHA of the previous deployment. Used by Gehen for rollback purposes.
	// Please do not modify this value.
	PreviousGitsha            string
	PreviousTaskDefinitionARN string
	TaskDefinitionARN         string
	Tags                      []string
}

// ScheduledTask represents an ECS Scheduled Task.
type ScheduledTask struct {
	Name                      string
	Gitsha                    string
	UpdateStrategy            string
	PreviousGitsha            string
	TaskDefinitionARN         string
	PreviousTaskDefinitionARN string
}

type ParsedConfig struct {
	Services       []*Service
	ScheduledTasks []*ScheduledTask
	Role           *Role
	TimeoutMinutes int
	UpdateStrategy string
}

// Read reads the config file at the given path and returns
// a slice of services and scheduled tasks.
func Read(configPath, gitsha string) (ParsedConfig, error) {
	if !file.Exists(configPath) {
		return ParsedConfig{}, errors.Errorf("no such file %s", configPath)
	}

	file, err := os.Open(configPath)
	if err != nil {
		return ParsedConfig{}, errors.Wrapf(err, "failed to open file %s", configPath)
	}
	defer file.Close()

	var config gehenConfig
	err = yaml.NewDecoder(file).Decode(&config)
	if err != nil {
		return ParsedConfig{}, errors.Wrapf(err, "couldn't read yaml file at %s", configPath)
	}

	updateStrategy := strings.ToLower(config.UpdateStrategy)
	switch updateStrategy {
	case UpdateStrategyCurrent, UpdateStrategyLatest, UpdateStrategyNone:
	case "":
		// Default is current
		updateStrategy = UpdateStrategyCurrent
	default:
		err := errors.Errorf(`config: invalid updateStrategy %q, must be "current", "latest" or "none`, config.UpdateStrategy)
		return ParsedConfig{}, err
	}

	var services []*Service
	for name, s := range config.Services {
		service := Service{
			Name:           name,
			Gitsha:         gitsha,
			Cluster:        s.Cluster,
			URL:            s.URL,
			UpdateStrategy: updateStrategy,
			Containers:     s.Containers,
		}
		services = append(services, &service)
	}

	var scheduledTasks []*ScheduledTask
	for name := range config.ScheduledTasks {
		task := ScheduledTask{
			Name:           name,
			Gitsha:         gitsha,
			UpdateStrategy: updateStrategy,
		}
		scheduledTasks = append(scheduledTasks, &task)
	}

	parsedConfig := ParsedConfig{
		Services:       services,
		ScheduledTasks: scheduledTasks,
		TimeoutMinutes: config.TimeoutMinutes,
		UpdateStrategy: updateStrategy,
	}

	if config.Role.ARN != "" {
		parsedConfig.Role = &config.Role
	}

	return parsedConfig, nil
}

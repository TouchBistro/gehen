package config

import (
	"os"

	"github.com/TouchBistro/goutils/file"
	"github.com/pkg/errors"
	"gopkg.in/yaml.v2"
)

type Service struct {
	Cluster        string   `yaml:"cluster"`
	URL            string   `yaml:"url"`
	TaskDefinition string   `yaml:"-"`
	Tags           []string `yaml:"-"`
}

type ServiceMap = map[string]Service

type GehenConfig struct {
	Services ServiceMap `yaml:"services"`
}

var config GehenConfig

func Init(path string) error {
	if !file.FileOrDirExists(path) {
		return errors.Errorf("No such file %s", path)
	}

	file, err := os.Open(path)
	if err != nil {
		return errors.Wrapf(err, "failed to open file %s", path)
	}
	defer file.Close()

	err = yaml.NewDecoder(file).Decode(&config)
	return errors.Wrapf(err, "couldn't read yaml file at %s", path)
}

func Config() *GehenConfig {
	return &config
}

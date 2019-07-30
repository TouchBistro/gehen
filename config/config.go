package config

import (
	"fmt"

	"github.com/TouchBistro/gehen/util"
	"github.com/pkg/errors"
)

type Service struct {
	Cluster string `yaml:"cluster"`
	URL     string `yaml:"url"`
}

type ServiceMap = map[string]Service

type GehenConfig struct {
	Services ServiceMap `yaml:"services"`
}

var config GehenConfig

func Init(path string) error {
	if !util.FileOrDirExists(path) {
		return fmt.Errorf("No such file %s", path)
	}

	err := util.ReadYaml(path, &config)
	return errors.Wrapf(err, "couldn't read yaml file at %s", path)
}

func Config() *GehenConfig {
	return &config
}

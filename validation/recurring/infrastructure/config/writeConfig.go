package config

import (
	"os"

	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

// WriteConfigToFile writes to the given cattle config file path.
func WriteConfigToFile(filePath string, cattleConfig map[string]any) {
	configBytes, err := yaml.Marshal(cattleConfig)
	if err != nil {
		panic(err)
	}

	file, err := os.Create(filePath)
	if err != nil {
		logrus.Fatalf("Failed to reset/overwrite the cattle config file. Error: %v", err)
	}

	_, err = file.Write(configBytes)
	if err != nil {
		logrus.Fatalf("Failed to reset/overwrite the cattle config file. Error: %v", err)
	}
}

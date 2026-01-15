package longhorn

import (
	"github.com/rancher/shepherd/pkg/config"
)

const (
	longhornTestConfigConfigurationFileKey = "longhorn"
	LonghornTestDefaultProject             = "longhorn-test"
	LonghornTestDefaultStorageClass        = "longhorn"
)

type TestConfig struct {
	LonghornTestProject      string `yaml:"testProject"`
	LonghornTestStorageClass string `yaml:"testStorageClass"`
}

// GetLonghornTestConfig gets a LonghornTestConfig object using the data from the config file.
// If any data is missing default values are used.
func GetLonghornTestConfig() *TestConfig {
	defer recover() // Recover from panic on LoadConfig in case no longhorn configuration is provided.

	longhornTestConfig := TestConfig{
		LonghornTestProject:      LonghornTestDefaultProject,
		LonghornTestStorageClass: LonghornTestDefaultStorageClass,
	}
	config.LoadConfig(longhornTestConfigConfigurationFileKey, &longhornTestConfig)

	return &longhornTestConfig
}

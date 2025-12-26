package longhorn

import (
	"fmt"
	"slices"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/tests/actions/storage"
)

// The json/yaml config key for the corral package to be build ..
const (
	longhornTestConfigConfigurationFileKey = "longhorn"
	LonghornTestDefaultProject             = "longhorn-test"
	LonghornTestDefaultStorageClass        = "longhorn"
)

type TestConfig struct {
	LonghornTestProject      string `json:"testProject,omitempty"`
	LonghornTestStorageClass string `json:"testStorageClass,omitempty"`
}

// GetLonghornTestConfig gets a LonghornTestConfig object using the data from the config file.
func GetLonghornTestConfig() *TestConfig {
	var longhornTestConfig TestConfig
	config.LoadConfig(longhornTestConfigConfigurationFileKey, &longhornTestConfig)

	defer func() {
		if r := recover(); r != nil {
			longhornTestConfig = TestConfig{
				LonghornTestProject:      LonghornTestDefaultProject,
				LonghornTestStorageClass: LonghornTestDefaultStorageClass,
			}
		} else {
			if !slices.Contains(storage.LonghornStorageClasses, longhornTestConfig.LonghornTestStorageClass) {
				panic(fmt.Errorf("Invalid storage class %s", longhornTestConfig.LonghornTestStorageClass))
			}
		}
	}()

	if longhornTestConfig.LonghornTestProject == "" {
		longhornTestConfig.LonghornTestProject = LonghornTestDefaultProject
	}

	if longhornTestConfig.LonghornTestStorageClass == "" {
		longhornTestConfig.LonghornTestStorageClass = LonghornTestDefaultStorageClass
	}

	return &longhornTestConfig
}

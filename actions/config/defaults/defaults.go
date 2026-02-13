package defaults

import (
	"os"
	"strings"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/sirupsen/logrus"
)

const (
	defaultFilePath = "defaults/defaults.yaml"
	RKE1            = "rke1"
	RKE2            = "rke2"
	K3S             = "k3s"
)

// LoadPackageDefaults loads the specified filename in the same package as the test
func LoadPackageDefaults(cattleConfig map[string]any, filePath string) (map[string]any, error) {
	var defaultsConfig map[string]any
	if filePath == "" {
		packagePath, err := os.Getwd()
		if err != nil {
			return nil, err
		}

		index := strings.LastIndex(packagePath, "/")
		parentPath := packagePath[:index+1]

		var packageDefaultsConfig map[string]any
		_, err = os.Stat(packagePath + "/" + defaultFilePath)
		if err == nil {
			packageDefaultsConfig = config.LoadConfigFromFile(packagePath + "/" + defaultFilePath)
		} else {
			logrus.Warningf("No defaults found in: %s", packagePath)
		}

		var parentDefaultsConfig map[string]any
		_, err = os.Stat(parentPath + defaultFilePath)
		if err == nil {
			parentDefaultsConfig = config.LoadConfigFromFile(parentPath + defaultFilePath)
			defaultsConfig, err = DeepMerge(packageDefaultsConfig, parentDefaultsConfig, true)
			if err != nil {
				return nil, err
			}
		} else {
			defaultsConfig = packageDefaultsConfig
			logrus.Warningf("No defaults found in: %s", parentPath)
		}
	}

	config, err := DeepMerge(cattleConfig, defaultsConfig, true)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// DeepMerge merges two maps together with priority given to the first map provided.
func DeepMerge(mergingMap map[string]any, baseMap map[string]any, OneToOneListMapping bool) (map[string]any, error) {
	output, err := operations.DeepCopyMap(baseMap)
	if err != nil {
		return nil, err
	}

	for k, v := range mergingMap {
		if _, ok := output[k].(map[string]any); ok {
			output[k], err = DeepMerge(mergingMap[k].(map[string]any), output[k].(map[string]any), OneToOneListMapping)
			if err != nil {
				return nil, err
			}
		} else if _, ok := output[k].([]any); ok {
			outputList := output[k].([]any)
			if _, ok := outputList[0].(map[string]any); ok && len(outputList) > 0 {
				var mergedList []map[string]any
				for i, mergingObject := range mergingMap[k].([]any) {
					var mergedOutput map[string]any
					if len(outputList) == len(mergingMap[k].([]any)) && OneToOneListMapping {
						mergedOutput, err = DeepMerge(mergingObject.(map[string]any), outputList[i].(map[string]any), OneToOneListMapping)
						if err != nil {
							return nil, err
						}
					} else {
						mergedOutput, err = DeepMerge(mergingObject.(map[string]any), outputList[0].(map[string]any), OneToOneListMapping)
						if err != nil {
							return nil, err
						}
					}
					if err != nil {
						return nil, err
					}

					mergedList = append(mergedList, mergedOutput)
				}

				output[k] = mergedList
			} else {
				output[k] = v
			}
		} else {
			output[k] = v
		}
	}

	return output, nil
}

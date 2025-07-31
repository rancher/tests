package defaults

import (
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
)

const (
	defaultFilePath = "defaults/defaults.yaml"
)

// LoadPackageDefaults loads the specified filename in the same package as the test
func LoadPackageDefaults(cattleConfig map[string]any, filePath string) (map[string]any, error) {
	if filePath == "" {
		filePath = defaultFilePath
	}

	defaultsConfig := config.LoadConfigFromFile(filePath)
	config, err := DeepMerge(cattleConfig, defaultsConfig)
	if err != nil {
		return nil, err
	}

	return config, nil
}

// DeepMerge merges two maps together with priority given to the first map provided.
func DeepMerge(mergingMap map[string]any, baseMap map[string]any) (map[string]any, error) {
	output, err := operations.DeepCopyMap(baseMap)
	if err != nil {
		return nil, err
	}

	for k, v := range mergingMap {
		if _, ok := output[k].(map[string]any); ok {
			output[k], err = DeepMerge(mergingMap[k].(map[string]any), output[k].(map[string]any))
			if err != nil {
				return nil, err
			}
		} else if _, ok := output[k].([]any); ok {
			outputList := output[k].([]any)
			if _, ok := outputList[0].(map[string]any); ok && len(outputList) > 0 {
				var mergedList []map[string]any
				for _, mergingObject := range mergingMap[k].([]any) {
					mergedOutput, err := DeepMerge(mergingObject.(map[string]any), outputList[0].(map[string]any))
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

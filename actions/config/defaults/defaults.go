package defaults

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/sirupsen/logrus"
)

const (
	DefaultFilePath = "defaults/defaults.yaml"
	RKE2            = "rke2"
	K3S             = "k3s"
)

var placeholderRegex = regexp.MustCompile(`<[^<>]+>`)

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
		_, err = os.Stat(packagePath + "/" + DefaultFilePath)
		packageDefaultsFound := err == nil
		if packageDefaultsFound {
			packageDefaultsConfig = config.LoadConfigFromFile(packagePath + "/" + DefaultFilePath)
		}

		var parentDefaultsConfig map[string]any
		_, err = os.Stat(parentPath + DefaultFilePath)
		parentDefaultsFound := err == nil
		if parentDefaultsFound {
			parentDefaultsConfig = config.LoadConfigFromFile(parentPath + DefaultFilePath)
			defaultsConfig, err = DeepMerge(packageDefaultsConfig, parentDefaultsConfig, true)
			if err != nil {
				return nil, err
			}
		} else {
			defaultsConfig = packageDefaultsConfig
		}

		if !packageDefaultsFound && !parentDefaultsFound {
			logrus.Warningf("No defaults found in: %s or %s", packagePath, parentPath)
		}
	} else {
		_, err := os.Stat(filePath)
		if err == nil {
			defaultsConfig = config.LoadConfigFromFile(filePath)
		} else {
			logrus.Warningf("No defaults found at: %s", filePath)
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

// VerifyCattleConfig checks for unresolved required and placeholder values.
// It logs an error for each required path and a warning for each placeholder path.
// ignoredPaths can include any sections to skip, such as []string{"prime"}.
func VerifyCattleConfig(cattleConfig map[string]any, ignoredPaths []string) error {
	requiredPaths, placeholderPaths := verifyConfigValue("", cattleConfig, ignoredPaths)

	for _, requiredPath := range requiredPaths {
		logrus.Errorf("Required config value is not set at path: %s", requiredPath)
	}

	for _, placeholderPath := range placeholderPaths {
		logrus.Warningf("Config value contains unresolved placeholder at path: %s", placeholderPath)
	}

	if len(requiredPaths) > 0 {
		return fmt.Errorf("required config values are not set at: %s", strings.Join(requiredPaths, ", "))
	}

	return nil
}

func shouldIgnorePath(path string, ignoredPaths []string) bool {
	for _, ignoredPath := range ignoredPaths {
		if ignoredPath == "" {
			continue
		}
		if path == ignoredPath || strings.HasPrefix(path, ignoredPath+".") || strings.HasPrefix(path, ignoredPath+"[") {
			return true
		}
	}
	return false
}

func verifyConfigValue(path string, value any, ignoredPaths []string) ([]string, []string) {
	var requiredPaths []string
	var placeholderPaths []string

	switch typedValue := value.(type) {
	case map[string]any:
		for key, nestedValue := range typedValue {
			nextPath := key
			if path != "" {
				nextPath = path + "." + key
			}
			if shouldIgnorePath(nextPath, ignoredPaths) {
				continue
			}
			nestedRequired, nestedPlaceholder := verifyConfigValue(nextPath, nestedValue, ignoredPaths)
			requiredPaths = append(requiredPaths, nestedRequired...)
			placeholderPaths = append(placeholderPaths, nestedPlaceholder...)
		}
	case []any:
		for i, nestedValue := range typedValue {
			nextPath := fmt.Sprintf("%s[%d]", path, i)
			if shouldIgnorePath(nextPath, ignoredPaths) {
				continue
			}
			nestedRequired, nestedPlaceholder := verifyConfigValue(nextPath, nestedValue, ignoredPaths)
			requiredPaths = append(requiredPaths, nestedRequired...)
			placeholderPaths = append(placeholderPaths, nestedPlaceholder...)
		}
	case string:
		if shouldIgnorePath(path, ignoredPaths) {
			return requiredPaths, placeholderPaths
		}
		if strings.Contains(typedValue, "<required>") {
			requiredPaths = append(requiredPaths, path)
		}

		if placeholderRegex.MatchString(typedValue) {
			placeholderPaths = append(placeholderPaths, path)
		}
	}

	return requiredPaths, placeholderPaths
}

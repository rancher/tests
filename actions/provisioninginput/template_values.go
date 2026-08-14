package provisioninginput

import (
	"fmt"
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"
)

// LoadTemplateValuesFile loads template chart values from a yaml file.
// If valuesFilePath is relative and configFilePath is provided, the path is resolved
// relative to the config file directory.
func LoadTemplateValuesFile(valuesFilePath, configFilePath string) (map[string]any, error) {
	resolvedPath := valuesFilePath
	if !filepath.IsAbs(valuesFilePath) && configFilePath != "" {
		resolvedPath = filepath.Join(filepath.Dir(configFilePath), valuesFilePath)
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read template values file %q: %w", resolvedPath, err)
	}

	values := map[string]any{}
	if err := yaml.Unmarshal(content, &values); err != nil {
		return nil, fmt.Errorf("failed to parse template values file %q: %w", resolvedPath, err)
	}

	return values, nil
}

// MergeMapValues deep-merges override values into base values and returns base.
// Scalar values and lists from override replace base values.
func MergeMapValues(base, override map[string]any) map[string]any {
	if base == nil {
		base = map[string]any{}
	}

	for key, value := range override {
		overrideMap, isOverrideMap := value.(map[string]any)
		if !isOverrideMap {
			base[key] = value
			continue
		}

		existingMap, isExistingMap := base[key].(map[string]any)
		if !isExistingMap {
			existingMap = map[string]any{}
		}

		base[key] = MergeMapValues(existingMap, overrideMap)
	}

	return base
}

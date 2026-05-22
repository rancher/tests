package config

import "github.com/sirupsen/logrus"

// UpdateAgentEnvVar is a helper function to update the value of an agentEnvVar in the cattle config.
func UpdateAgentEnvVar(config map[string]any, name, value string) {
	clusterConfig, ok := config["clusterConfig"].(map[string]any)
	if !ok {
		logrus.Fatalf("clusterConfig not found or not a map")
	}

	envVars, ok := clusterConfig["agentEnvVars"].([]any)
	if !ok {
		logrus.Fatalf("agentEnvVars not found or not a slice")
	}

	for _, v := range envVars {
		envVar, ok := v.(map[string]any)
		if !ok {
			continue
		}

		if envVar["name"] == name {
			envVar["value"] = value
		}

	}
}

// UpdateRegistryVars is a helper function to update the registry configs in the cattle config.
func UpdateRegistryVars(config map[string]any, url string) {
	clusterConfig, ok := config["clusterConfig"].(map[string]any)
	if !ok {
		logrus.Fatalf("clusterConfig not found or not a map")
	}

	registries, ok := clusterConfig["registries"].(map[string]any)
	if !ok {
		logrus.Fatalf("registries not found or not a map")
	}

	rke2Registries, ok := registries["rke2Registries"].(map[string]any)
	if !ok {
		logrus.Fatalf("rke2Registries not found or not a map")
	}

	configs, ok := rke2Registries["configs"].(map[string]any)
	if !ok {
		logrus.Fatalf("configs not found or not a map")
	}

	for k := range configs {
		delete(configs, k)
	}

	rke2Registries["configs"] = map[string]any{
		url: map[string]any{
			"insecureSkipVerify": true,
		},
	}
}

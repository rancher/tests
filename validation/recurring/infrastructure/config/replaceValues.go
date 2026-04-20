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

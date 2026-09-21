package config

import (
	"fmt"
	"strings"
)

// OverrideRancherInstanceType applies the dedicated Rancher instance size to the Terraform AWS config.
func OverrideRancherInstanceType(cattleConfig map[string]any) error {
	terraformConfig, ok := cattleConfig["terraform"].(map[string]any)
	if !ok {
		return fmt.Errorf("terraform config is missing")
	}

	provider, _ := terraformConfig["provider"].(string)
	if !strings.EqualFold(provider, "aws") {
		return nil
	}

	awsConfig, ok := terraformConfig["awsConfig"].(map[string]any)
	if !ok {
		return fmt.Errorf("terraform awsConfig is missing")
	}

	rancherInstance, ok := awsConfig["rancherInstance"].(string)
	if !ok || rancherInstance == "" {
		rancherInstance, ok = awsConfig["downstreamAWSInstanceType"].(string)
	}
	if !ok || rancherInstance == "" {
		return fmt.Errorf("terraform awsConfig rancherInstance is missing")
	}

	awsConfig["awsInstanceType"] = rancherInstance
	return nil
}

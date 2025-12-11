package main

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/framework"
	"github.com/rancher/tfp-automation/framework/cleanup"
	"github.com/rancher/tfp-automation/framework/set/resources/dualstack"
	"github.com/rancher/tfp-automation/framework/set/resources/ipv6"
	"github.com/rancher/tfp-automation/framework/set/resources/rancher2"
	"github.com/rancher/tfp-automation/tests/infrastructure/ranchers"
	"github.com/sirupsen/logrus"
)

func main() {
	var client *rancher.Client
	var err error

	t := &testing.T{}

	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))
	rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(cattleConfig)

	switch {
	case terraformConfig.AWSConfig.EnablePrimaryIPv6:
		client, err = setupIPv6Rancher(t, rancherConfig, terraformConfig, terratestConfig)
		if err != nil {
			logrus.Fatalf("Failed to setup Rancher: %v", err)
		}
	case !terraformConfig.AWSConfig.EnablePrimaryIPv6 && terraformConfig.AWSConfig.ClusterCIDR != "":
		client, err = setupDualstackRancher(t, rancherConfig, terraformConfig, terratestConfig)
		if err != nil {
			logrus.Fatalf("Failed to setup Rancher: %v", err)
		}
	default:
		client, _, _, _, _, err = setupRancher(t)
		if err != nil {
			logrus.Fatalf("Failed to setup Rancher: %v", err)
		}
	}

	_, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)
}

func setupRancher(t *testing.T) (*rancher.Client, string, *terraform.Options, *terraform.Options, map[string]any, error) {
	testSession := session.NewSession()
	client, serverNodeOne, standaloneTerraformOptions, terraformOptions, cattleConfig := ranchers.SetupRancher(t, testSession, keypath.SanityKeyPath)

	return client, serverNodeOne, standaloneTerraformOptions, terraformOptions, cattleConfig, nil
}

func setupIPv6Rancher(t *testing.T, rancherConfig *rancher.Config, terraformConfig *tfpConfig.TerraformConfig,
	terratestConfig *tfpConfig.TerratestConfig) (*rancher.Client, error) {
	_, keyPath := rancher2.SetKeyPath(keypath.IPv6KeyPath, terratestConfig.PathToRepo, terraformConfig.Provider)
	terraformOptions := framework.Setup(t, terraformConfig, terratestConfig, keyPath)

	_, err := ipv6.CreateMainTF(t, terraformOptions, keyPath, rancherConfig, terraformConfig, terratestConfig)
	if err != nil {
		return nil, err
	}

	testSession := session.NewSession()

	client, err := ranchers.PostRancherSetup(t, terraformOptions, rancherConfig, testSession, terraformConfig.Standalone.RancherHostname, keyPath, false)
	if err != nil && *rancherConfig.Cleanup {
		cleanup.Cleanup(nil, terraformOptions, keyPath)
	}

	return client, nil
}

func setupDualstackRancher(t *testing.T, rancherConfig *rancher.Config, terraformConfig *tfpConfig.TerraformConfig,
	terratestConfig *tfpConfig.TerratestConfig) (*rancher.Client, error) {
	_, keyPath := rancher2.SetKeyPath(keypath.DualStackKeyPath, terratestConfig.PathToRepo, terraformConfig.Provider)
	terraformOptions := framework.Setup(t, terraformConfig, terratestConfig, keyPath)

	_, err := dualstack.CreateMainTF(t, terraformOptions, keyPath, rancherConfig, terraformConfig, terratestConfig)
	if err != nil {
		return nil, err
	}

	testSession := session.NewSession()

	client, err := ranchers.PostRancherSetup(t, terraformOptions, rancherConfig, testSession, terraformConfig.Standalone.RancherHostname, keyPath, false)
	if err != nil && *rancherConfig.Cleanup {
		cleanup.Cleanup(nil, terraformOptions, keyPath)
	}

	return client, nil
}

package main

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/tests/infrastructure/ranchers"
	"github.com/sirupsen/logrus"
)

func main() {
	var client *rancher.Client
	var err error

	t := &testing.T{}

	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))
	_, terraformConfig, _, _ := tfpConfig.LoadTFPConfigs(cattleConfig)

	testSession := session.NewSession()

	switch {
	case terraformConfig.AWSConfig.AWSVpcIP != "":
		var registry, bastion string

		client, registry, bastion, err = setupAirgapRancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup Airgap Rancher: %v", err)
		}

		_, err = operations.ReplaceValue([]string{"terraform", "airgapBastion"}, bastion, cattleConfig)
		if err != nil {
			logrus.Fatalf("Failed to replace airgap bastion: %v", err)
		}

		_, err = operations.ReplaceValue([]string{"terraform", "privateRegistries", "systemDefaultRegistry"}, registry, cattleConfig)
		if err != nil {
			logrus.Fatalf("Failed to replace system default registry: %v", err)
		}

		infraConfig.UpdateRegistryVars(cattleConfig, registry)
	case !terraformConfig.AWSConfig.EnablePrimaryIPv6 && terraformConfig.AWSConfig.ClusterCIDR != "":
		client, err = setupDualStackRancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup Dual Stack Rancher: %v", err)
		}
	case terraformConfig.AWSConfig.EnablePrimaryIPv6:
		client, err = setupIPv6Rancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup IPv6 Rancher: %v", err)
		}
	case terraformConfig.Proxy != nil:
		var proxyBastion string

		client, proxyBastion, err = setupProxyRancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup Proxy Rancher: %v", err)
		}

		_, err = operations.ReplaceValue([]string{"terraform", "proxy", "proxyBastion"}, proxyBastion, cattleConfig)
		if err != nil {
			logrus.Fatalf("Failed to replace proxy bastion: %v", err)
		}

		infraConfig.UpdateAgentEnvVar(cattleConfig, "HTTP_PROXY", "http://"+proxyBastion+":3228")
		infraConfig.UpdateAgentEnvVar(cattleConfig, "HTTPS_PROXY", "http://"+proxyBastion+":3228")
	default:
		client, err = setupRancher(t, testSession)
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

func setupAirgapRancher(t *testing.T, testSession *session.Session) (*rancher.Client, string, string, error) {
	client, registry, bastion, _, _, _, _ := ranchers.SetupAirgapRancher(t, testSession, keypath.AirgapKeyPath)

	return client, registry, bastion, nil
}

func setupDualStackRancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, _, _, _, _ := ranchers.SetupDualStackRancher(t, testSession, keypath.DualStackKeyPath)

	return client, nil
}

func setupIPv6Rancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, _, _, _, _ := ranchers.SetupIPv6Rancher(t, testSession, keypath.IPv6KeyPath)

	return client, nil
}

func setupProxyRancher(t *testing.T, testSession *session.Session) (*rancher.Client, string, error) {
	client, proxyBastion, _, _, _, _ := ranchers.SetupProxyRancher(t, testSession, keypath.ProxyKeyPath)

	return client, proxyBastion, nil
}

func setupRancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, _, _, _, _ := ranchers.SetupRancher(t, testSession, keypath.SanityKeyPath)

	return client, nil
}

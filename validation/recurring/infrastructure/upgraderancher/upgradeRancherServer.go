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
		client, err = upgradeAirgapRancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup Airgap Rancher: %v", err)
		}
	case !terraformConfig.AWSConfig.EnablePrimaryIPv6 && terraformConfig.AWSConfig.ClusterCIDR != "":
		client, err = upgradeDualStackRancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup Dual Stack Rancher: %v", err)
		}
	case terraformConfig.AWSConfig.EnablePrimaryIPv6:
		client, err = upgradeIPv6Rancher(t, testSession)
		if err != nil {
			logrus.Fatalf("Failed to setup IPv6 Rancher: %v", err)
		}
	case terraformConfig.Proxy != nil:
		var proxyBastion string

		client, proxyBastion, err = upgradeProxyRancher(t, testSession)
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
		client, err = upgradeRancher(t, testSession)
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

func upgradeAirgapRancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, registry, bastion, _, _, cattleConfig, _ := ranchers.SetupAirgapRancher(t, testSession, keypath.AirgapKeyPath)
	client, _, _, _ = ranchers.UpgradeAirgapRancher(t, client, bastion, registry, testSession, cattleConfig, nil)

	return client, nil
}

func upgradeDualStackRancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, serverNodeOne, _, _, cattleConfig := ranchers.SetupDualStackRancher(t, testSession, keypath.DualStackKeyPath)
	client, _, _, _ = ranchers.UpgradeDualStackRancher(t, client, serverNodeOne, testSession, cattleConfig)

	return client, nil
}

func upgradeIPv6Rancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, serverNodeOne, _, _, cattleConfig := ranchers.SetupIPv6Rancher(t, testSession, keypath.IPv6KeyPath)
	client, _, _, _ = ranchers.UpgradeIPv6Rancher(t, client, serverNodeOne, testSession, cattleConfig)

	return client, nil
}

func upgradeProxyRancher(t *testing.T, testSession *session.Session) (*rancher.Client, string, error) {
	client, proxyBastion, proxyPrivateIP, _, _, cattleConfig := ranchers.SetupProxyRancher(t, testSession, keypath.ProxyKeyPath)
	client, _, _, _ = ranchers.UpgradeProxyRancher(t, client, proxyPrivateIP, proxyBastion, testSession, cattleConfig)

	return client, proxyBastion, nil
}

func upgradeRancher(t *testing.T, testSession *session.Session) (*rancher.Client, error) {
	client, serverNodeOne, _, _, cattleConfig := ranchers.SetupRancher(t, testSession, keypath.SanityKeyPath)
	client, _, _, _ = ranchers.UpgradeRancher(t, client, serverNodeOne, testSession, cattleConfig)

	return client, nil
}

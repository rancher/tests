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
	"github.com/rancher/tfp-automation/tests/infrastructure"
	"github.com/sirupsen/logrus"
)

func main() {
	var client *rancher.Client
	var err error

	t := &testing.T{}

	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))
	_, _, _, _ = tfpConfig.LoadTFPConfigs(cattleConfig)

	switch {
	default:
		client, err = upgradeRancher(t)
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

func upgradeRancher(t *testing.T) (*rancher.Client, error) {
	testSession := session.NewSession()

	client, serverNodeOne, _, _, cattleConfig := infrastructure.SetupRancher(t, testSession, keypath.SanityKeyPath)
	client, _, _, _ = infrastructure.UpgradeRancher(t, client, serverNodeOne, testSession, cattleConfig)

	return client, nil

}

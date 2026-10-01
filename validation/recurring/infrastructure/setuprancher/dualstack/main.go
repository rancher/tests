package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupdualstack "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/dualstack"
	"github.com/sirupsen/logrus"
)

func main() {
	t := &testing.T{}

	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))

	_, currentFilePath, _, ok := runtime.Caller(0)
	if !ok {
		logrus.Fatal("Failed to determine current file path")
	}

	providerName, err := provisioning.GetProviderFromConfig(cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to get provider from config: %v", err)
	}

	packageDefaultsPath := filepath.Join(filepath.Dir(currentFilePath), "defaults", providerName+".yaml")

	cattleConfig, err = defaults.LoadPackageDefaults(cattleConfig, packageDefaultsPath, providerName)
	if err != nil {
		logrus.Fatalf("Failed to load package defaults: %v", err)
	}

	cattleConfig, err = defaults.LoadSecretsManagerDefaults(cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to load Secrets Manager defaults: %v", err)
	}

	err = defaults.VerifyCattleConfig(cattleConfig, nil)
	if err != nil {
		logrus.Fatalf("Cattle config verification failed: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)

	testSession := session.NewSession()

	client, _, _, _, _ := setupdualstack.SetupDualStackRancher(t, testSession, keypath.DualStackKeyPath, cattleConfig)

	cattleConfig, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)
}

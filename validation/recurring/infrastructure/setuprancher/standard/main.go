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
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupstandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/standard"
	"github.com/sirupsen/logrus"
)

func main() {
	t := &testing.T{}

	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))

	_, currentFilePath, _, ok := runtime.Caller(0)
	if !ok {
		logrus.Fatal("Failed to determine current file path")
	}

	packageDefaultsPath := filepath.Join(filepath.Dir(currentFilePath), defaults.DefaultFilePath)

	cattleConfig, err := defaults.LoadPackageDefaults(cattleConfig, packageDefaultsPath)
	if err != nil {
		logrus.Fatalf("Failed to load package defaults: %v", err)
	}

	cattleConfig, err = defaults.LoadSecretsManagerDefaults(cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to load Secrets Manager defaults: %v", err)
	}

	err = defaults.VerifyCattleConfig(cattleConfig)
	if err != nil {
		logrus.Fatalf("Cattle config verification failed: %v", err)
	}

	testSession := session.NewSession()

	client, _, _, _, _ := setupstandard.SetupRancher(t, testSession, keypath.SanityKeyPath, cattleConfig)

	cattleConfig, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)
}

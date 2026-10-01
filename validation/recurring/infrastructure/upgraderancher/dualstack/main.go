package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupdualstack "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/dualstack"
	upgradedualstack "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/upgrade/dualstack"
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

	testSession := session.NewSession()

	client, serverNodeOne, _, _, cattleConfig := setupdualstack.SetupDualStackRancher(t, testSession, keypath.DualStackKeyPath, cattleConfig)

	cattleConfig, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetLocal + "/local")
	if err != nil {
		logrus.Fatalf("Failed to retrieve local cluster: %v", err)
	}

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(client, cluster)
	if err != nil {
		logrus.Fatalf("Failed to verify cluster deployments: %v", err)
	}

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(client, cluster)
	if err != nil {
		logrus.Fatalf("Failed to verify cluster pods: %v", err)
	}

	client, _, _, _ = upgradedualstack.UpgradeDualStackRancher(t, client, serverNodeOne, testSession, cattleConfig)

	cattleConfig, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(client, cluster)
	if err != nil {
		logrus.Fatalf("Failed to verify cluster deployments after upgrade: %v", err)
	}

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(client, cluster)
	if err != nil {
		logrus.Fatalf("Failed to verify cluster pods after upgrade: %v", err)
	}
}

package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	"github.com/rancher/tests/validation/recurring/infrastructure/upgraderancher/clusters"
	"github.com/rancher/tests/validation/recurring/infrastructure/upgraderancher/localcluster"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupstandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/standard"
	upgradestandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/upgrade/standard"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
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

	testSession := session.NewSession()

	client, serverNodeOne, _, _, cattleConfig := setupstandard.SetupRancher(t, testSession, keypath.SanityKeyPath, cattleConfig)

	cattleConfig, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetLocal + "/local")
	require.NoError(t, err)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(client, cluster)
	require.NoError(t, err)

	// If awsEC2Configs is present in the cattleConfig, provision clusters before upgrading Rancher.
	// If it is not present, skip provisioning clusters and proceed with upgrading Rancher.
	if _, ok := cattleConfig[ec2.ConfigurationFileKey]; ok {
		clusters.ProvisionClustersPreUpgrade(t, client, cattleConfig)
	}

	client, _, _, _ = upgradestandard.UpgradeRancher(t, client, serverNodeOne, testSession, cattleConfig)

	cattleConfig, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, client.RancherConfig.AdminToken, cattleConfig)
	if err != nil {
		logrus.Fatalf("Failed to replace admin token: %v", err)
	}

	infraConfig.WriteConfigToFile(os.Getenv(config.ConfigEnvironmentKey), cattleConfig)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(client, cluster)
	require.NoError(t, err)

	_, terraform, _, standalone := tfpConfig.LoadTFPConfigs(cattleConfig)

	if standalone.UpgradeLocalCluster {
		err = localcluster.UpgradeLocalCluster(client, terraform)
		require.NoError(t, err)
	}
}

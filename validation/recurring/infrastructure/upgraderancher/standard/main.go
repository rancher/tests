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
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	"github.com/rancher/tests/validation/recurring/infrastructure/upgraderancher/clusters"
	v3cluster "github.com/rancher/tests/validation/recurring/infrastructure/upgraderancher/v3cluster"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupstandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/standard"
	upgradestandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/upgrade/standard"
	"github.com/sirupsen/logrus"
)

func main() {
	t := &testing.T{}

	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))

	_, currentFilePath, _, ok := runtime.Caller(0)
	if !ok {
		logrus.Fatal("Failed to determine current file path")
	}

	providerName, _, err := provisioning.GetProviderFromConfig(cattleConfig)
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

	client, serverNodeOne, _, _, cattleConfig := setupstandard.SetupRancher(t, testSession, keypath.SanityKeyPath, cattleConfig)

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
	if err != nil {
		logrus.Fatalf("Failed to verify cluster deployments after upgrade: %v", err)
	}

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(client, cluster)
	if err != nil {
		logrus.Fatalf("Failed to verify cluster pods after upgrade: %v", err)
	}

	logrus.Info("Verifying existing clusters are ready")
	err = provisioning.VerifyExistingClustersReady(client)
	if err != nil {
		logrus.Fatalf("Failed to verify existing clusters are ready after upgrade: %v", err)
	}

	logrus.Info("Verifying existing v3 clusters are ready")
	err = provisioning.VerifyExistingClustersReadyV3(client)
	if err != nil {
		logrus.Fatalf("Failed to verify existing v3 clusters are ready after upgrade: %v", err)
	}

	logrus.Info("Verifying existing cluster deployments")
	err = deployment.VerifyExistingClustersDeployments(client)
	if err != nil {
		logrus.Fatalf("Failed to verify existing cluster deployments after upgrade: %v", err)
	}

	logrus.Info("Verifying existing cluster pods")
	err = pods.VerifyExistingClustersPods(client)
	if err != nil {
		logrus.Fatalf("Failed to verify existing cluster pods after upgrade: %v", err)
	}

	_, _, _, standalone := tfpConfig.LoadTFPConfigs(cattleConfig)

	if standalone.UpgradeLocalCluster {
		err = v3cluster.UpgradeV3Cluster(client, "local")
		if err != nil {
			logrus.Fatalf("Failed to upgrade local cluster: %v", err)
		}
	}
}

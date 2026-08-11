package main

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	infraConfig "github.com/rancher/tests/validation/recurring/infrastructure/config"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	tfpCustom "github.com/rancher/tfp-automation/tests/infrastructure/downstream/custom"
	tfpImported "github.com/rancher/tfp-automation/tests/infrastructure/downstream/imported"
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
	if _, ok := cattleConfig[ec2.ConfigurationFileKey]; !ok {
		return
	} else {
		provisionClustersPreUpgrade(t, client, cattleConfig)
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
}

func provisionClustersPreUpgrade(t *testing.T, client *rancher.Client, cattleConfig map[string]any) {
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(cattleConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, cattleConfig, awsEC2Configs)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	rancher, terraform, terratest, _ := tfpConfig.LoadTFPConfigs(cattleConfig)

	clusterConfig.MachinePools = nodeRolesStandard

	customNodepoolsRKE2 := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true}, {Quantity: 1, Controlplane: true}, {Quantity: 1, Worker: true}, {Quantity: 1, Windows: true}}
	customNodepoolsK3S := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true}, {Quantity: 1, Controlplane: true}, {Quantity: 1, Worker: true}}
	importedNodepools := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	addErr := func(err error) {
		mu.Lock()
		errs = append(errs, err)
		mu.Unlock()
	}

	for _, clusterType := range []string{defaults.RKE2, defaults.K3S} {
		clusterType := clusterType

		wg.Add(1)
		go func() {
			defer wg.Done()
			logrus.Infof("Provisioning %s node driver cluster", clusterType)
			_, err := resources.ProvisionRKE2K3SCluster(t, client, clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
			if err != nil {
				addErr(fmt.Errorf("%s node driver: %w", clusterType, err))
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			terraformCustom := *terraform
			terratestCustom := *terratest

			if clusterType == defaults.RKE2 {
				terratestCustom.Nodepools = customNodepoolsRKE2
			} else {
				terratestCustom.Nodepools = customNodepoolsK3S
			}

			logrus.Infof("Provisioning %s custom cluster", clusterType)
			_, _, _, customCluster := tfpCustom.CreateCustomCluster(t, client, rancher, &terraformCustom, &terratestCustom, clusterType, "validation/provisioning/"+clusterType, false)
			if err := provisioning.VerifyClusterReady(client, customCluster); err != nil {
				addErr(fmt.Errorf("%s custom: %w", clusterType, err))
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			terraformImported := *terraform
			terratestImported := *terratest
			terratestImported.Nodepools = importedNodepools

			logrus.Infof("Provisioning %s imported cluster", clusterType)
			_, _, _, importedCluster := tfpImported.CreateImportedCluster(t, client, rancher, &terraformImported, &terratestImported, clusterType, "validation/provisioning/"+clusterType)
			if err := provisioning.VerifyClusterReadyV3(client, importedCluster.Name); err != nil {
				addErr(fmt.Errorf("%s imported: %w", clusterType, err))
			}
		}()
	}

	wg.Wait()

	for _, err := range errs {
		require.NoError(t, err)
	}
}

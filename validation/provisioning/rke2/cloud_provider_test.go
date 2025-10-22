//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults/providers"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type cloudProviderTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func cloudProviderSetup(t *testing.T) cloudProviderTest {
	var r cloudProviderTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	assert.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	assert.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, r.cattleConfig)
	assert.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	assert.NoError(t, err)

	return r
}

func TestAWSCloudProvider(t *testing.T) {
	t.Parallel()
	r := cloudProviderSetup(t)

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated[0].MachinePoolConfig.Quantity = 3
	nodeRolesDedicated[1].MachinePoolConfig.Quantity = 2
	nodeRolesDedicated[2].MachinePoolConfig.Quantity = 2

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
	}{
		{"AWS_OutOfTree", nodeRolesDedicated, r.standardUserClient},
	}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
	if clusterConfig.Provider != "aws" {
		t.Skip("AWS Cloud Provider test requires access to aws.")
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig.CloudProvider = providers.AWS
			clusterConfig.MachinePools = tt.machinePools

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			logrus.Infof("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			assert.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, r.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(t, r.client, cluster)

			logrus.Infof("Verifying cloud provider (%s)", cluster.Name)
			provider.VerifyCloudProviderFunc(t, r.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestVSphereCloudProvider(t *testing.T) {
	r := cloudProviderSetup(t)

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated[0].MachinePoolConfig.Quantity = 3
	nodeRolesDedicated[1].MachinePoolConfig.Quantity = 2
	nodeRolesDedicated[2].MachinePoolConfig.Quantity = 2

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
	}{
		{"vSphere_OutOfTree", nodeRolesDedicated, r.standardUserClient},
	}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
	if clusterConfig.Provider != "vsphere" {
		t.Skip("Vsphere Cloud Provider test requires access to vsphere.")
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig.CloudProvider = "rancher-vsphere"
			clusterConfig.MachinePools = tt.machinePools

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			assert.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, r.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(t, r.client, cluster)

			logrus.Infof("Verifying cloud provider (%s)", cluster.Name)
			provider.VerifyCloudProviderFunc(t, r.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestHarvesterCloudProvider(t *testing.T) {
	t.Parallel()
	r := cloudProviderSetup(t)

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated[0].MachinePoolConfig.Quantity = 1
	nodeRolesDedicated[1].MachinePoolConfig.Quantity = 2
	nodeRolesDedicated[2].MachinePoolConfig.Quantity = 2

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
	}{
		{"Harvester_oot", nodeRolesDedicated, r.client},
	}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
	if clusterConfig.Provider != "harvester" {
		t.Skip("Harvester Cloud Provider test requires access to harvester.")
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig.Provider = providers.Harvester
			clusterConfig.MachinePools = tt.machinePools

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			logrus.Infof("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			assert.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, r.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(t, r.client, cluster)

			logrus.Infof("Verifying cloud provider (%s)", cluster.Name)
			provider.VerifyCloudProviderFunc(t, r.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

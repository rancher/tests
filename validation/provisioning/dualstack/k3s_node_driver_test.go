//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
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

type nodeDriverK3STest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func nodeDriverK3SSetup(t *testing.T) nodeDriverK3STest {
	var k nodeDriverK3STest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)
	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	assert.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	assert.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(k.client, defaults.K3S, k.cattleConfig)
	assert.NoError(t, err)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	assert.NoError(t, err)

	return k
}

func TestNodeDriverK3S(t *testing.T) {
	t.Skip("This test is temporarily disabled. See https://github.com/rancher/rancher/issues/51844.")
	t.Parallel()
	k := nodeDriverK3SSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

	ipv6Params := &machinepools.AWSMachineConfig{
		EnablePrimaryIpv6: true,
		HttpProtocolIpv6:  "enabled",
		Ipv6AddressCount:  "1",
		Ipv6AddressOnly:   true,
	}

	nonIpv6Params := &machinepools.AWSMachineConfig{
		EnablePrimaryIpv6: false,
		HttpProtocolIpv6:  "disabled",
		Ipv6AddressCount:  "",
		Ipv6AddressOnly:   false,
	}

	cidr := &provisioninginput.Networking{
		ClusterCIDR: clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR: clusterConfig.Networking.ServiceCIDR,
	}

	cidrDualStackPreference := &provisioninginput.Networking{
		ClusterCIDR:     clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR:     clusterConfig.Networking.ServiceCIDR,
		StackPreference: "dual",
	}

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
		ipv6Params   *machinepools.AWSMachineConfig
		networking   *provisioninginput.Networking
	}{
		{"K3S_Dual_Stack_Node_Driver_CIDR", k.standardUserClient, nodeRolesStandard, nonIpv6Params, cidr},
		{"K3S_Dual_Stack_Node_Driver_CIDR_Dual_Stack_Preference", k.standardUserClient, nodeRolesStandard, ipv6Params, cidrDualStackPreference},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.machinePools
			clusterConfig.Networking = tt.networking

			machineConfig := new(machinepools.AWSMachineConfigs)

			config.LoadAndUpdateConfig(machinepools.AWSMachineConfigsKey, machineConfig, func() {
				for i := range machineConfig.AWSMachineConfig {
					machineConfig.AWSMachineConfig[i].EnablePrimaryIpv6 = tt.ipv6Params.EnablePrimaryIpv6
					machineConfig.AWSMachineConfig[i].HttpProtocolIpv6 = tt.ipv6Params.HttpProtocolIpv6
					machineConfig.AWSMachineConfig[i].Ipv6AddressCount = tt.ipv6Params.Ipv6AddressCount
					machineConfig.AWSMachineConfig[i].Ipv6AddressOnly = tt.ipv6Params.Ipv6AddressOnly
				}
			})

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			assert.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, tt.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(t, tt.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, k.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

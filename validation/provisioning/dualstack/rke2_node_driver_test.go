//go:build validation || (recurring && dualstack) || dualstack

package dualstack

import (
	"os"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type nodeDriverRKE2Test struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func nodeDriverRKE2Setup(t *testing.T) nodeDriverRKE2Test {
	var r nodeDriverRKE2Test
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestNodeDriverRKE2(t *testing.T) {
	t.Parallel()
	r := nodeDriverRKE2Setup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

	cidr := &provisioninginput.Networking{
		ClusterCIDR: clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR: clusterConfig.Networking.ServiceCIDR,
	}

	cidrDualStackPreference := &provisioninginput.Networking{
		ClusterCIDR:     clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR:     clusterConfig.Networking.ServiceCIDR,
		StackPreference: "dual",
	}

	ipv6FirstDualStackPreference := &provisioninginput.Networking{
		ClusterCIDR:     clusterConfig.Networking.IPV6FirstClusterCIDR,
		ServiceCIDR:     clusterConfig.Networking.IPV6FirstServiceCIDR,
		StackPreference: "dual",
	}

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
		networking   *provisioninginput.Networking
	}{
		{"RKE2_Dual_Stack_Node_Driver_CIDR", r.standardUserClient, nodeRolesStandard, cidr},
		{"RKE2_Dual_Stack_Node_Driver_CIDR_Dual_Stack_Preference", r.standardUserClient, nodeRolesStandard, cidrDualStackPreference},
		{"RKE2_Dual_Stack_Node_Driver_CIDR_IPv6_First_Dual_Stack_Preference", r.standardUserClient, nodeRolesStandard, ipv6FirstDualStackPreference},
		{"RKE2_Dual_Stack_Node_Driver_CIDR_IPv6_Address_Only", r.standardUserClient, nodeRolesStandard, cidrDualStackPreference},
		{"RKE2_Dual_Stack_Node_Driver_CIDR_IPv6_Address_Only_IPv6_First", r.standardUserClient, nodeRolesStandard, ipv6FirstDualStackPreference},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.machinePools
			clusterConfig.Networking = tt.networking

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

			// Needed to ensure we are testing use-case with IPv6 address only set and without it set in the other test cases.
			if strings.Contains(tt.name, "IPv6_Address_Only") || strings.Contains(tt.name, "IPv6_First") {
				machineConfigSpec.AmazonEC2MachineConfigs.AWSMachineConfig[0].Ipv6AddressOnly = true
				t.Skip("Skipping test due to issue with AWS ipv6 address only provisioning; see GH issue: https://github.com/rancher/rancher/issues/54944")
			}

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(tt.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

//go:build validation || recurring

package ipv6

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
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type nodeDriverRKE2IPv6Test struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func nodeDriverRKE2IPv6Setup(t *testing.T) nodeDriverRKE2IPv6Test {
	var r nodeDriverRKE2IPv6Test

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

func TestNodeDriverRKE2IPv6(t *testing.T) {
	t.Parallel()
	r := nodeDriverRKE2IPv6Setup(t)

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

	stackPreference := &provisioninginput.Networking{
		ClusterCIDR:     "",
		ServiceCIDR:     "",
		StackPreference: "ipv6",
	}

	cidrStackPreference := &provisioninginput.Networking{
		ClusterCIDR:     clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR:     clusterConfig.Networking.ServiceCIDR,
		StackPreference: "ipv6",
	}

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
		networking   *provisioninginput.Networking
	}{
		{"RKE2_IPv6_Node_Driver_CIDR", r.standardUserClient, nodeRolesStandard, cidr},
		{"RKE2_IPv6_Node_Driver_Stack_Preference", r.standardUserClient, nodeRolesStandard, stackPreference},
		{"RKE2_IPv6_Node_Driver_CIDR_Stack_Preference", r.standardUserClient, nodeRolesStandard, cidrStackPreference},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools
		clusterConfig.Networking = tt.networking

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, tt.client, cluster)

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

//go:build validation || recurring

package k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
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

type customTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func customSetup(t *testing.T) customTest {
	var k customTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(k.client, defaults.K3S, k.cattleConfig)
	require.NoError(t, err)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	require.NoError(t, err)

	return k
}

func TestCustom(t *testing.T) {
	t.Parallel()
	k := customSetup(t)

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
	}{
		{"K3S_Custom|etcd_cp_worker", k.standardUserClient, nodeRolesAll},
		{"K3S_Custom|etcd_cp|worker", k.standardUserClient, nodeRolesShared},
		{"K3S_Custom|etcd|cp|worker", k.standardUserClient, nodeRolesDedicated},
		{"K3S_Custom|3_etcd|2_cp|3_worker", k.standardUserClient, nodeRolesStandard},
	}
	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.session.Cleanup()
		})

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, k.cattleConfig, awsEC2Configs)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
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

		params := provisioning.GetCustomSchemaParams(tt.client, k.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

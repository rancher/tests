//go:build validation || recurring

package ipv6

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/deleting/rke2k3s"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DeleteInitMachineIPv6TestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	cattleConfig  map[string]any
	rke2ClusterID string
}

func (d *DeleteInitMachineIPv6TestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteInitMachineIPv6TestSuite) SetupSuite() {
	testSession := session.NewSession()
	d.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(d.T(), err)

	d.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(d.client)
	require.NoError(d.T(), err)

	d.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, d.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(d.T(), err)

	rke2ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, rke2ClusterConfig)

	rke2ClusterConfig.Networking = &provisioninginput.Networking{
		ClusterCIDR:     rke2ClusterConfig.Networking.ClusterCIDR,
		ServiceCIDR:     rke2ClusterConfig.Networking.ServiceCIDR,
		StackPreference: rke2ClusterConfig.Networking.StackPreference,
	}

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	rke2ClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster")
	d.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2ClusterConfig, nil, true, false)
	require.NoError(d.T(), err)
}

func (d *DeleteInitMachineIPv6TestSuite) TestDeleteInitMachineIPv6() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2_IPv6_Delete_Init_Machine", d.rke2ClusterID},
	}

	for _, tt := range tests {
		cluster, err := d.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(d.T(), err)

		d.Run(tt.name, func() {
			logrus.Infof("Deleting init machine on cluster (%s)", cluster.Name)
			err := rke2k3s.DeleteInitMachine(d.client, tt.clusterID)
			require.NoError(d.T(), err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(d.T(), d.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(d.T(), d.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(d.client, d.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestDeleteInitMachineIPv6TestSuite(t *testing.T) {
	suite.Run(t, new(DeleteInitMachineIPv6TestSuite))
}

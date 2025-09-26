//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DeleteDualstackClusterTestSuite struct {
	suite.Suite
	session                *session.Session
	client                 *rancher.Client
	cattleConfig           map[string]any
	rke2IPv4ClusterID      string
	k3sIPv4ClusterID       string
	rke2DualstackClusterID string
	k3sDualstackClusterID  string
}

func (d *DeleteDualstackClusterTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteDualstackClusterTestSuite) SetupSuite() {
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

	rke2IPv4ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, rke2IPv4ClusterConfig)

	rke2IPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	rke2DualstackClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, rke2DualstackClusterConfig)

	rke2DualstackClusterConfig.IPv6Cluster = true
	rke2DualstackClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "dual",
	}

	k3sIPv4ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, k3sIPv4ClusterConfig)

	k3sIPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	k3sDualstackClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, k3sDualstackClusterConfig)

	k3sDualstackClusterConfig.IPv6Cluster = true
	k3sDualstackClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "dual",
	}

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	rke2IPv4ClusterConfig.MachinePools = nodeRolesStandard
	rke2DualstackClusterConfig.MachinePools = nodeRolesStandard
	k3sIPv4ClusterConfig.MachinePools = nodeRolesStandard
	k3sDualstackClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster w/ipv4 stack preference")
	d.rke2IPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2IPv4ClusterConfig, nil, true, false)
	require.NoError(d.T(), err)

	logrus.Info("Provisioning RKE2 cluster w/dual stack preference")
	d.rke2DualstackClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2DualstackClusterConfig, nil, true, false)
	require.NoError(d.T(), err)

	logrus.Info("Provisioning K3S cluster w/ipv4 stack preference")
	d.k3sIPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sIPv4ClusterConfig, nil, true, false)
	require.NoError(d.T(), err)

	logrus.Info("Provisioning K3S cluster w/dual stack preference")
	d.k3sDualstackClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sDualstackClusterConfig, nil, true, false)
	require.NoError(d.T(), err)
}

func (d *DeleteDualstackClusterTestSuite) TestDeletingDualstackCluster() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2_Delete_IPv4_Cluster", d.rke2IPv4ClusterID},
		{"RKE2_Delete_Dualstack_Cluster", d.rke2DualstackClusterID},
		{"K3S_Delete_IPv4_Cluster", d.k3sIPv4ClusterID},
		{"K3S_Delete_Dualstack_Cluster", d.k3sDualstackClusterID},
	}

	for _, tt := range tests {
		d.Run(tt.name, func() {
			logrus.Infof("Deleting cluster (%s)", tt.clusterID)
			extClusters.DeleteK3SRKE2Cluster(d.client, tt.clusterID)

			logrus.Infof("Verifying cluster (%s) deletion", tt.clusterID)
			provisioning.VerifyDeleteRKE2K3SCluster(d.T(), d.client, tt.clusterID)
		})

		params := provisioning.GetProvisioningSchemaParams(d.client, d.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestDeleteDualstackClusterTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteDualstackClusterTestSuite))
}

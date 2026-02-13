//go:build validation || extended

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/rancher/shepherd/extensions/clusters"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/storage"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type UpgradeCloudProviderSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	rke1ClusterID string
}

func (u *UpgradeCloudProviderSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeCloudProviderSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(u.T(), err)

	u.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(u.client)
	require.NoError(u.T(), err)

	nodeRolesStandard := []provisioninginput.NodePools{
		provisioninginput.EtcdNodePool,
		provisioninginput.ControlPlaneNodePool,
		provisioninginput.WorkerNodePool,
	}

	nodeRolesStandard[0].NodeRoles.Quantity = 3
	nodeRolesStandard[1].NodeRoles.Quantity = 2
	nodeRolesStandard[2].NodeRoles.Quantity = 3

	provisioningConfig.NodePools = nodeRolesStandard

	u.rke1ClusterID, err = resources.ProvisionRKE1Cluster(u.T(), standardUserClient, provisioningConfig, false, false)
	require.NoError(u.T(), err)
}

func (u *UpgradeCloudProviderSuite) TestVsphere() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE1 vSphere migration", u.rke1ClusterID},
	}

	for _, tt := range tests {
		cluster, err := u.client.Management.Cluster.ByID(tt.clusterID)
		require.NoError(u.T(), err)

		_, _, err = extClusters.GetProvisioningClusterByName(u.client, cluster.Name, namespaces.FleetDefault)
		require.NoError(u.T(), err)

		u.Run(tt.name, func() {
			logrus.Info("Starting upgrade test...")
			err := charts.UpgradeVsphereOutOfTreeCharts(u.client, catalog.RancherChartRepo, cluster.Name)
			require.NoError(u.T(), err)

			clusterID, err := clusters.GetClusterIDByName(u.client, cluster.Name)
			require.NoError(u.T(), err)

			podErrors := pods.StatusPods(u.client, clusterID)
			require.Empty(u.T(), podErrors)

			storage.CreatePVCWorkload(u.T(), u.client, clusterID, "")
		})
	}
}

func TestCloudProviderVersionUpgradeSuite(t *testing.T) {
	suite.Run(t, new(UpgradeCloudProviderSuite))
}

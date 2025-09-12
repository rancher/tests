//go:build validation || extended

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/rancher/tests/validation/upgrade"
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
			upgrade.VsphereCloudProviderCharts(u.T(), u.client, u.client.RancherConfig.ClusterName)
		})
	}
}

func TestCloudProviderVersionUpgradeSuite(t *testing.T) {
	suite.Run(t, new(UpgradeCloudProviderSuite))
}

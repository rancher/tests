//go:build (validation || infra.rke1 || cluster.custom || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !cluster.any && !cluster.nodedriver && !sanity && !extended

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/provisioninginput"
	nodepools "github.com/rancher/tests/actions/rke1/nodepools"
	"github.com/rancher/tests/actions/scalinginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RKE1CustomClusterNodeScalingTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	scalingConfig *scalinginput.Config
	rke1ClusterID string
}

func (s *RKE1CustomClusterNodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *RKE1CustomClusterNodeScalingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	nodeRolesStandard := []provisioninginput.NodePools{
		provisioninginput.EtcdNodePool,
		provisioninginput.ControlPlaneNodePool,
		provisioninginput.WorkerNodePool,
	}

	nodeRolesStandard[0].NodeRoles.Quantity = 3
	nodeRolesStandard[1].NodeRoles.Quantity = 2
	nodeRolesStandard[2].NodeRoles.Quantity = 3

	provisioningConfig.NodePools = nodeRolesStandard

	s.rke1ClusterID, err = resources.ProvisionRKE1Cluster(s.T(), standardUserClient, provisioningConfig, true, true)
	require.NoError(s.T(), err)
}

func (s *RKE1CustomClusterNodeScalingTestSuite) TestScalingRKE1CustomClusterNodes() {
	nodeRolesEtcd := nodepools.NodeRoles{
		Etcd:     true,
		Quantity: 1,
	}

	nodeRolesControlPlane := nodepools.NodeRoles{
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesEtcdControlPlane := nodepools.NodeRoles{
		Etcd:         true,
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesWorker := nodepools.NodeRoles{
		Worker:   true,
		Quantity: 1,
	}

	tests := []struct {
		name      string
		nodeRoles nodepools.NodeRoles
		clusterID string
	}{
		{"RKE1_Custom_Scale_Control_Plane", nodeRolesControlPlane, s.rke1ClusterID},
		{"RKE1_Custom_Scale_ETCD", nodeRolesEtcd, s.rke1ClusterID},
		{"RKE1_Custom_Scale_Control_Plane_ETCD", nodeRolesEtcdControlPlane, s.rke1ClusterID},
		{"RKE1_Custom_Scale_Worker", nodeRolesWorker, s.rke1ClusterID},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			ScalingRKE1CustomClusterPools(s.T(), s.client, tt.clusterID, s.scalingConfig.NodeProvider, tt.nodeRoles)
		})
	}
}

func (s *RKE1CustomClusterNodeScalingTestSuite) TestScalingRKE1CustomClusterNodesDynamicInput() {
	if s.scalingConfig.MachinePools.NodeRoles == nil {
		s.T().Skip()
	}

	clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	ScalingRKE1CustomClusterPools(s.T(), s.client, clusterID, s.scalingConfig.NodeProvider, *s.scalingConfig.NodePools.NodeRoles)
}

func TestRKE1CustomClusterNodeScalingTestSuite(t *testing.T) {
	suite.Run(t, new(RKE1CustomClusterNodeScalingTestSuite))
}

//go:build (validation || infra.rke1 || cluster.nodedriver || extended) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !cluster.any && !cluster.custom && !sanity && !stress

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

type RKE1NodeScalingTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	scalingConfig *scalinginput.Config
	rke1ClusterID string
}

func (s *RKE1NodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *RKE1NodeScalingTestSuite) SetupSuite() {
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

	s.rke1ClusterID, err = resources.ProvisionRKE1Cluster(s.T(), standardUserClient, provisioningConfig, true, false)
	require.NoError(s.T(), err)
}

func (s *RKE1NodeScalingTestSuite) TestScalingRKE1NodePools() {
	nodeRolesEtcd := nodepools.NodeRoles{
		Etcd:     true,
		Quantity: 1,
	}

	nodeRolesControlPlane := nodepools.NodeRoles{
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
		{"RKE1_Node_Driver_Scale_Control_Plane", nodeRolesControlPlane, s.rke1ClusterID},
		{"RKE1_Node_Driver_Scale_ETCD", nodeRolesEtcd, s.rke1ClusterID},
		{"RKE1_Node_Driver_Scale_Worker", nodeRolesWorker, s.rke1ClusterID},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			ScalingRKE1NodePools(s.T(), s.client, tt.clusterID, tt.nodeRoles)
		})
	}
}

func (s *RKE1NodeScalingTestSuite) TestScalingRKE1NodePoolsDynamicInput() {
	if s.scalingConfig.NodePools.NodeRoles == nil {
		s.T().Skip()
	}

	clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	ScalingRKE1NodePools(s.T(), s.client, clusterID, *s.scalingConfig.NodePools.NodeRoles)
}

func TestRKE1NodeScalingTestSuite(t *testing.T) {
	suite.Run(t, new(RKE1NodeScalingTestSuite))
}

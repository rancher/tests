//go:build (validation || infra.rke1 || cluster.nodedriver || extended) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !cluster.any && !cluster.custom && !sanity && !stress

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
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

type RKE1NodeReplacingTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	rke1ClusterID string
}

func (s *RKE1NodeReplacingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *RKE1NodeReplacingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, err := standard.CreateStandardUser(s.client)
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

func (s *RKE1NodeReplacingTestSuite) TestReplacingRKE1Nodes() {
	nodeRolesEtcd := nodepools.NodeRoles{
		Etcd:         true,
		ControlPlane: false,
		Worker:       false,
	}

	nodeRolesControlPlane := nodepools.NodeRoles{
		Etcd:         false,
		ControlPlane: true,
		Worker:       false,
	}

	nodeRolesWorker := nodepools.NodeRoles{
		Etcd:         false,
		ControlPlane: false,
		Worker:       true,
	}

	tests := []struct {
		name      string
		nodeRoles nodepools.NodeRoles
		clusterID string
	}{
		{"RKE1_Node_Driver_Replace_Control_Plane", nodeRolesControlPlane, s.rke1ClusterID},
		{"RKE1_Node_Driver_Replace_ETCD", nodeRolesEtcd, s.rke1ClusterID},
		{"RKE1_Node_Driver_Replace_Worker", nodeRolesWorker, s.rke1ClusterID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Management.Cluster.ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			err := scalinginput.ReplaceRKE1Nodes(s.client, cluster.Name, tt.nodeRoles.Etcd, tt.nodeRoles.ControlPlane, tt.nodeRoles.Worker)
			require.NoError(s.T(), err)
		})
	}
}

func TestRKE1NodeReplacingTestSuite(t *testing.T) {
	suite.Run(t, new(RKE1NodeReplacingTestSuite))
}

//go:build (validation || infra.rke2k3s || cluster.nodedriver || extended) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !sanity && !stress

package nodescaling

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeReplacingTestSuite struct {
	suite.Suite
	session *session.Session
	client  *rancher.Client
}

func (s *NodeReplacingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *NodeReplacingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *NodeReplacingTestSuite) TestReplacingNodes() {
	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd: true,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker: true,
	}

	tests := []struct {
		name        string
		clusterType string
		nodeRoles   machinepools.NodeRoles
		client      *rancher.Client
	}{
		{"RKE2_Node_Driver_Replace_Control_Plane", "rke2", nodeRolesControlPlane, s.client},
		{"RKE2_Node_Driver_Replace_ETCD", "rke2", nodeRolesEtcd, s.client},
		{"RKE2_Node_Driver_Replace_Worker", "rke2", nodeRolesWorker, s.client},
		{"K3S_Node_Driver_Replace_Control_Plane", "k3s", nodeRolesControlPlane, s.client},
		{"K3S_Node_Driver_Replace_ETCD", "k3s", nodeRolesEtcd, s.client},
		{"K3S_Node_Driver_Replace_Worker", "k3s", nodeRolesWorker, s.client},
	}

	for _, tt := range tests {
		existingClusterType, err := actionsClusters.GetClusterType(tt.client, s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				s.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			err := scalinginput.ReplaceNodes(s.client, s.client.RancherConfig.ClusterName, tt.nodeRoles.Etcd, tt.nodeRoles.ControlPlane, tt.nodeRoles.Worker)
			require.NoError(s.T(), err)
		})
	}
}

func TestNodeReplacingTestSuite(t *testing.T) {
	suite.Run(t, new(NodeReplacingTestSuite))
}

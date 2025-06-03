//go:build (validation || infra.rke2k3s || cluster.custom || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !cluster.any && !cluster.nodedriver && !sanity && !extended

package nodescaling

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CustomClusterNodeScalingTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	scalingConfig *scalinginput.Config
}

func (s *CustomClusterNodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *CustomClusterNodeScalingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *CustomClusterNodeScalingTestSuite) TestScalingCustomClusterNodes() {
	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd:     true,
		Quantity: 1,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesEtcdControlPlane := machinepools.NodeRoles{
		Etcd:         true,
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker:   true,
		Quantity: 1,
	}

	nodeRolesWindows := machinepools.NodeRoles{
		Windows:  true,
		Quantity: 1,
	}

	tests := []struct {
		name        string
		clusterType string
		nodeRoles   machinepools.NodeRoles
		client      *rancher.Client
		isWindows   bool
	}{
		{"RKE2_Custom_Scale_Control_Plane", "rke2", nodeRolesControlPlane, s.client, false},
		{"RKE2_Custom_Scale_ETCD", "rke2", nodeRolesEtcd, s.client, false},
		{"RKE2_Custom_Scale_Control_Plane_ETCD", "rke2", nodeRolesEtcdControlPlane, s.client, false},
		{"RKE2_Custom_Scale_Worker", "rke2", nodeRolesWorker, s.client, false},
		{"RKE2_Custom_Scale_Windows", "rke2", nodeRolesWindows, s.client, true},
		{"K3S_Custom_Scale_Control_Plane", "k3s", nodeRolesControlPlane, s.client, false},
		{"K3S_Custom_Scale_ETCD", "k3s", nodeRolesEtcd, s.client, false},
		{"K3S_Custom_Scale_Control_Plane_ETCD", "k3s", nodeRolesEtcdControlPlane, s.client, false},
		{"K3S_Custom_Scale_Worker", "k3s", nodeRolesWorker, s.client, false},
	}

	for _, tt := range tests {
		clusterID, err := clusters.GetV1ProvisioningClusterByName(s.client, s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)

		existingClusterType, err := actionsClusters.GetClusterType(tt.client, s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				s.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			scalingRKE2K3SCustomClusterPools(s.T(), s.client, clusterID, s.scalingConfig.NodeProvider, tt.nodeRoles)
		})
	}
}

func (s *CustomClusterNodeScalingTestSuite) TestScalingCustomClusterNodesDynamicInput() {
	if s.scalingConfig.MachinePools.NodeRoles == nil {
		s.T().Skip()
	}

	clusterID, err := clusters.GetV1ProvisioningClusterByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	scalingRKE2K3SCustomClusterPools(s.T(), s.client, clusterID, s.scalingConfig.NodeProvider, *s.scalingConfig.MachinePools.NodeRoles)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestCustomClusterNodeScalingTestSuite(t *testing.T) {
	suite.Run(t, new(CustomClusterNodeScalingTestSuite))
}

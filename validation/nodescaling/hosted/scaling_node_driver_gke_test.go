//go:build (validation || infra.gke || extended) && !infra.any && !infra.aks && !infra.eks && !infra.rke2k3s && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !sanity && !stress

package hosted

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/gke"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GKENodeScalingTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	scalingConfig *scalinginput.Config
}

func (s *GKENodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *GKENodeScalingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *GKENodeScalingTestSuite) TestScalingGKENodePools() {
	var oneNode int64 = 1
	var twoNodes int64 = 2

	scaleOneNode := gke.NodePool{
		InitialNodeCount: &oneNode,
	}

	scaleTwoNodes := gke.NodePool{
		InitialNodeCount: &twoNodes,
	}

	tests := []struct {
		name     string
		gkeNodes gke.NodePool
	}{
		{"GKE_Scale_Node_Group_By_1", scaleOneNode},
		{"GKE_Scale_Node_Group_By_2", scaleTwoNodes},
	}

	for _, tt := range tests {
		clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			ScalingGKENodePools(s.T(), s.client, clusterID, &tt.gkeNodes)
		})
	}
}

func (s *GKENodeScalingTestSuite) TestScalingGKENodePoolsDynamicInput() {
	if s.scalingConfig.GKENodePool == nil {
		s.T().Skip()
	}

	clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	ScalingGKENodePools(s.T(), s.client, clusterID, s.scalingConfig.GKENodePool)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestGKENodeScalingTestSuite(t *testing.T) {
	t.Skip("This test has been deprecated; check https://github.com/rancher/hosted-providers-e2e for updated tests")
	suite.Run(t, new(GKENodeScalingTestSuite))
}

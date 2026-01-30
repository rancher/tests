//go:build (validation || infra.eks || extended) && !infra.any && !infra.aks && !infra.gke && !infra.rke2k3s && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !sanity && !stress

package hosted

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/eks"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type EKSNodeScalingTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	scalingConfig *scalinginput.Config
}

func (s *EKSNodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *EKSNodeScalingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *EKSNodeScalingTestSuite) TestScalingEKSNodePools() {
	var oneNode int64 = 1
	var twoNodes int64 = 2

	scaleOneNode := eks.NodeGroupConfig{
		DesiredSize: &oneNode,
	}

	scaleTwoNodes := eks.NodeGroupConfig{
		DesiredSize: &twoNodes,
	}

	tests := []struct {
		name     string
		eksNodes eks.NodeGroupConfig
	}{
		{"EKS_Scale_Node_Group_By_1", scaleOneNode},
		{"EKS_Scale_Node_Group_By_2", scaleTwoNodes},
	}

	for _, tt := range tests {
		clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			ScalingEKSNodePools(s.T(), s.client, clusterID, &tt.eksNodes)
		})
	}
}

func (s *EKSNodeScalingTestSuite) TestScalingEKSNodePoolsDynamicInput() {
	if s.scalingConfig.EKSNodePool == nil {
		s.T().Skip()
	}

	clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	ScalingEKSNodePools(s.T(), s.client, clusterID, s.scalingConfig.EKSNodePool)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestEKSNodeScalingTestSuite(t *testing.T) {
	t.Skip("This test has been deprecated; check https://github.com/rancher/hosted-providers-e2e for updated tests")
	suite.Run(t, new(EKSNodeScalingTestSuite))
}

//go:build (infra.rke2k3s || validation) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !stress && !sanity && !extended

package deleting

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ClusterDeleteTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (c *ClusterDeleteTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *ClusterDeleteTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client
}

func (c *ClusterDeleteTestSuite) TestDeletingCluster() {
	tests := []struct {
		name        string
		clusterType string
		client      *rancher.Client
	}{
		{"RKE1_Delete_Cluster", "rke1", c.client},
		{"RKE2_Delete_Cluster", "rke2", c.client},
		{"K3S_Delete_Cluster", "k3s", c.client},
	}

	for _, tt := range tests {
		clusterID, err := clusters.GetV1ProvisioningClusterByName(tt.client, tt.client.RancherConfig.ClusterName)
		require.NoError(c.T(), err)

		existingClusterType, err := actionsClusters.GetClusterType(tt.client, tt.client.RancherConfig.ClusterName)
		require.NoError(c.T(), err)

		c.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				c.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			if tt.clusterType == "rke1" {
				clusters.DeleteRKE1Cluster(tt.client, clusterID)
				provisioning.VerifyDeleteRKE1Cluster(c.T(), tt.client, clusterID)
			} else {
				clusters.DeleteK3SRKE2Cluster(tt.client, clusterID)
				provisioning.VerifyDeleteRKE2K3SCluster(c.T(), tt.client, clusterID)
			}

		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestClusterDeleteTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterDeleteTestSuite))
}

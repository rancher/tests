//go:build (validation || infra.rke2k3s || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package certificates

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CertRotationTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *provisioninginput.Config
}

func (c *CertRotationTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CertRotationTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	c.clustersConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, c.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client
}

func (c *CertRotationTestSuite) TestCertRotation() {
	tests := []struct {
		name        string
		clusterType string
		rotations   int
		client      *rancher.Client
	}{
		{"RKE1_Certificate_Rotation", "rke1", 2, c.client},
		{"RKE2_Certificate_Rotation", "rke2", 2, c.client},
		{"K3S_Certificate_Rotation", "k3s", 2, c.client},
	}

	for _, tt := range tests {
		existingClusterType, err := clusters.GetClusterType(tt.client, c.client.RancherConfig.ClusterName)
		require.NoError(c.T(), err)

		c.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				c.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			for i := 0; i < tt.rotations; i++ {
				if tt.clusterType == "rke1" {
					err := rotateRKE1Certs(c.client, c.client.RancherConfig.ClusterName)
					require.NoError(c.T(), err)
				} else {
					err := rotateCerts(c.client, c.client.RancherConfig.ClusterName)
					require.NoError(c.T(), err)
				}
			}
		})
	}
}

func TestCertRotationTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationTestSuite))
}

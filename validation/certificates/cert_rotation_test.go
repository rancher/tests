//go:build (validation || infra.rke2k3s || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package certificates

import (
	"strings"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
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
	clusterID, err := clusters.GetV1ProvisioningClusterByName(c.client, c.client.RancherConfig.ClusterName)
	require.NoError(c.T(), err)

	cluster, err := c.client.Steve.SteveType(provisioningSteveResourceType).ByID(clusterID)
	require.NoError(c.T(), err)

	spec := &provv1.ClusterSpec{}
	err = steveV1.ConvertToK8sType(cluster.Spec, spec)
	require.NoError(c.T(), err)

	clusterType := "RKE1"

	if strings.Contains(spec.KubernetesVersion, "-rancher") || len(spec.KubernetesVersion) == 0 {
		c.Run(clusterType+" cert rotation", func() {
			require.NoError(c.T(), rotateRKE1Certs(c.client, c.client.RancherConfig.ClusterName))
			require.NoError(c.T(), rotateRKE1Certs(c.client, c.client.RancherConfig.ClusterName))
		})
	} else {
		if strings.Contains(spec.KubernetesVersion, "k3s") {
			clusterType = "K3s"
		} else {
			clusterType = "RKE2"
		}

		c.Run(clusterType+" cert rotation", func() {
			require.NoError(c.T(), rotateCerts(c.client, c.client.RancherConfig.ClusterName))
			require.NoError(c.T(), rotateCerts(c.client, c.client.RancherConfig.ClusterName))
		})
	}
}

func TestCertRotationTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationTestSuite))
}

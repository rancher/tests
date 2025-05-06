//go:build (validation || infra.rke2k3s || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package certificates

import (
	"slices"
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

type CertRotationWindowsTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *provisioninginput.Config
}

func (c *CertRotationWindowsTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CertRotationWindowsTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	c.clustersConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, c.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client
}

func (c *CertRotationWindowsTestSuite) TestCertRotationWindows() {
	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"RKE2 Windows cert rotation", c.client},
	}

	for _, tt := range tests {
		if !slices.Contains(c.clustersConfig.Providers, "vsphere") {
			c.T().Skip("Test requires vSphere provider")
		}

		clusterID, err := clusters.GetV1ProvisioningClusterByName(c.client, c.client.RancherConfig.ClusterName)
		require.NoError(c.T(), err)

		cluster, err := c.client.Steve.SteveType(provisioningSteveResourceType).ByID(clusterID)
		require.NoError(c.T(), err)

		spec := &provv1.ClusterSpec{}
		err = steveV1.ConvertToK8sType(cluster.Spec, spec)
		require.NoError(c.T(), err)

		windowsMachinePool := false
		for _, machinePool := range spec.RKEConfig.MachinePools {
			if machinePool.Labels["cattle.io/os"] == "windows" {
				windowsMachinePool = true
				break
			}
		}

		if !windowsMachinePool {
			c.T().Skip("Skipping test - no Windows machine pool found")
		}

		c.Run(tt.name, func() {
			require.NoError(c.T(), rotateCerts(c.client, c.client.RancherConfig.ClusterName))
			require.NoError(c.T(), rotateCerts(c.client, c.client.RancherConfig.ClusterName))
		})
	}
}

func TestCertRotationWindowsTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationWindowsTestSuite))
}

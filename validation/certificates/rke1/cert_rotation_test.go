//go:build (validation || infra.rke2k3s || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/certificates"
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RKE1CertRotationTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	rke1ClusterID string
}

func (c *RKE1CertRotationTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *RKE1CertRotationTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(c.client)
	require.NoError(c.T(), err)

	nodeRolesStandard := []provisioninginput.NodePools{
		provisioninginput.EtcdNodePool,
		provisioninginput.ControlPlaneNodePool,
		provisioninginput.WorkerNodePool,
	}

	nodeRolesStandard[0].NodeRoles.Quantity = 3
	nodeRolesStandard[1].NodeRoles.Quantity = 2
	nodeRolesStandard[2].NodeRoles.Quantity = 3

	provisioningConfig.NodePools = nodeRolesStandard

	c.rke1ClusterID, err = resources.ProvisionRKE1Cluster(c.T(), standardUserClient, provisioningConfig, true, false)
	require.NoError(c.T(), err)
}

func (c *RKE1CertRotationTestSuite) TestRKE1CertRotation() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE1_Certificate_Rotation", c.rke1ClusterID},
	}

	for _, tt := range tests {
		cluster, err := c.client.Management.Cluster.ByID(tt.clusterID)
		require.NoError(c.T(), err)

		c.Run(tt.name, func() {
			require.NoError(c.T(), certificates.RotateRKE1Certs(c.client, cluster.Name))
		})
	}
}

func TestRKE1CertRotationTestSuite(t *testing.T) {
	suite.Run(t, new(RKE1CertRotationTestSuite))
}

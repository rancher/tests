//go:build (infra.rke2k3s || validation) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !stress && !sanity && !extended

package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DeleteRKE1ClusterTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	rke1ClusterID string
}

func (d *DeleteRKE1ClusterTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteRKE1ClusterTestSuite) SetupSuite() {
	testSession := session.NewSession()
	d.session = testSession

	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(d.T(), err)

	d.client = client

	standardUserClient, err := standard.CreateStandardUser(d.client)
	require.NoError(d.T(), err)

	nodeRolesStandard := []provisioninginput.NodePools{
		provisioninginput.EtcdNodePool,
		provisioninginput.ControlPlaneNodePool,
		provisioninginput.WorkerNodePool,
	}

	nodeRolesStandard[0].NodeRoles.Quantity = 3
	nodeRolesStandard[1].NodeRoles.Quantity = 2
	nodeRolesStandard[2].NodeRoles.Quantity = 3

	provisioningConfig.NodePools = nodeRolesStandard

	d.rke1ClusterID, err = resources.ProvisionRKE1Cluster(d.T(), standardUserClient, provisioningConfig, true, false)
	require.NoError(d.T(), err)
}

func (d *DeleteRKE1ClusterTestSuite) TestDeletingRKE1Cluster() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE1_Delete_Cluster", d.rke1ClusterID},
	}

	for _, tt := range tests {
		d.Run(tt.name, func() {
			clusters.DeleteRKE1Cluster(d.client, tt.clusterID)
			provisioning.VerifyDeleteRKE1Cluster(d.T(), d.client, tt.clusterID)
		})
	}
}

func TestDeleteRKE1ClusterTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteRKE1ClusterTestSuite))
}

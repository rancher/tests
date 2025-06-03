//go:build (infra.rke2k3s || validation) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !stress && !sanity && !extended

package deleting

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	ProvisioningSteveResourceType = "provisioning.cattle.io.cluster"
)

type DeleteInitMachineTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (d *DeleteInitMachineTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteInitMachineTestSuite) SetupSuite() {
	testSession := session.NewSession()
	d.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(d.T(), err)

	d.client = client
}

func (d *DeleteInitMachineTestSuite) TestDeleteInitMachine() {
	tests := []struct {
		name        string
		clusterType string
		client      *rancher.Client
	}{
		{"RKE2_Delete_Init_Machine", "rke2", d.client},
		{"K3S_Delete_Init_Machine", "k3s", d.client},
	}
	for _, tt := range tests {
		clusterID, err := clusters.GetV1ProvisioningClusterByName(tt.client, tt.client.RancherConfig.ClusterName)
		require.NoError(d.T(), err)

		existingClusterType, err := actionsClusters.GetClusterType(tt.client, tt.client.RancherConfig.ClusterName)
		require.NoError(d.T(), err)

		d.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				d.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			deleteInitMachine(d.T(), tt.client, clusterID)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestDeleteInitMachineTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteInitMachineTestSuite))
}

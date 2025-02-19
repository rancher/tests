//go:build (validation || infra.any || cluster.any || stress) && !sanity && !extended && (2.8 || 2.9 || 2.10)

package snapshotrbac

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/projects"
	rbac "github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	restrictedAdmin rbac.Role = "restricted-admin"
)

func (etcd *SnapshotRBACTestSuite) TearDownSuite() {
	etcd.session.Cleanup()
}

func (etcd *SnapshotRBACTestSuite) SetupSuite() {
	etcd.session = session.NewSession()

	client, err := rancher.NewClient("", etcd.session)
	require.NoError(etcd.T(), err)

	etcd.client = client
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(etcd.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(etcd.client, clusterName)
	require.NoError(etcd.T(), err, "Error getting cluster ID")
	etcd.cluster, err = etcd.client.Management.Cluster.ByID(clusterID)
	require.NoError(etcd.T(), err)
}

func (etcd *SnapshotRBACTestSuite) testRKE2K3SSnapshotRBAC(role string, standardUserClient *rancher.Client) {
	log.Info("Test case - Take Etcd snapshot of a cluster as a " + role)
	_, err := etcdsnapshot.CreateRKE2K3SSnapshot(standardUserClient, etcd.cluster.Name)

	require.NoError(etcd.T(), err)

}

func (etcd *SnapshotRBACTestSuite) TestRKE2K3SSnapshotRBAC() {
	subSession := etcd.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		name   string
		role   string
		member string
	}{
		{"Restricted Admin", restrictedAdmin.String(), restrictedAdmin.String()},
	}
	for _, tt := range tests {
		if !(strings.Contains(etcd.cluster.ID, "c-m-")) {
			etcd.T().Skip("Skipping tests since cluster is not of type - k3s or RKE2")
		}
		etcd.Run("Set up User with Role "+tt.name, func() {
			clusterUser, clusterClient, err := rbac.SetupUser(etcd.client, tt.member)
			require.NoError(etcd.T(), err)

			adminProject, err := etcd.client.Management.Project.Create(projects.NewProjectConfig(etcd.cluster.ID))
			require.NoError(etcd.T(), err)

			if tt.member == rbac.StandardUser.String() {
				if strings.Contains(tt.role, "project") {
					err := users.AddProjectMember(etcd.client, adminProject, clusterUser, tt.role, nil)
					require.NoError(etcd.T(), err)
				} else {
					err := users.AddClusterRoleToUser(etcd.client, etcd.cluster, clusterUser, tt.role, nil)
					require.NoError(etcd.T(), err)
				}
			}

			relogin, err := clusterClient.ReLogin()
			require.NoError(etcd.T(), err)
			clusterClient = relogin

			etcd.testRKE2K3SSnapshotRBAC(tt.role, clusterClient)
		})
	}
}

func TestRASnapshotRBACTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRBACTestSuite))
}

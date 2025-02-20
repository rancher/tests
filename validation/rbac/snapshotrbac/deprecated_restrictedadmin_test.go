//go:build (validation || infra.any || cluster.any || stress) && !sanity && !extended && (2.8 || 2.9 || 2.10)

package snapshotrbac

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/tests/actions/projects"
	rbac "github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	restrictedAdmin rbac.Role = "restricted-admin"
)

func (etcd *SnapshotRBACTestSuite) TestRKE2K3SSnapshotRBACRA() {
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

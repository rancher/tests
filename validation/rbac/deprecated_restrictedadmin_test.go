//go:build (validation || infra.any || cluster.any || sanity) && !stress && !extended && (2.8 || 2.9 || 2.10)

package rbac

import (
	"strings"
	"testing"

	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

func (rb *RBTestSuite) sequentialTestRBACRA(role rbac.Role, member string, user *management.User) {
	standardClient, err := rb.client.AsUser(user)
	require.NoError(rb.T(), err)

	adminProject, err := rb.client.Management.Project.Create(projects.NewProjectConfig(rb.cluster.ID))
	require.NoError(rb.T(), err)

	if member == rbac.StandardUser.String() {
		if strings.Contains(role.String(), "project") {
			err := users.AddProjectMember(rb.client, adminProject, user, role.String(), nil)
			require.NoError(rb.T(), err)
		} else {
			err := users.AddClusterRoleToUser(rb.client, rb.cluster, user, role.String(), nil)
			require.NoError(rb.T(), err)
		}
	}

	standardClient, err = standardClient.ReLogin()
	require.NoError(rb.T(), err)

	additionalUser, err := users.CreateUserWithRole(rb.client, users.UserConfig(), rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	rb.Run("Validating Global Role Binding is created for "+role.String(), func() {
		verifyRAGlobalRoleBindingsForUser(rb.T(), user, rb.client)
	})
	rb.Run("Validating corresponding role bindings for users", func() {
		verifyRARoleBindingsForUser(rb.T(), user, rb.client, rb.cluster.ID)
	})
	rb.Run("Validating if "+role.String()+" can list any downstream clusters", func() {
		verifyRAUserCanListCluster(rb.T(), rb.client, standardClient, rb.cluster.ID, role)
	})
	rb.Run("Validating if members with role "+role.String()+" are able to list all projects", func() {
		verifyRAUserCanListProject(rb.T(), rb.client, standardClient, rb.cluster.ID)
	})
	rb.Run("Validating if members with role "+role.String()+" is able to create a project in the cluster", func() {
		verifyRAUserCanCreateProjects(rb.T(), standardClient, rb.cluster.ID, role)
	})
	rb.Run("Validate namespaces checks for members with role "+role.String(), func() {
		verifyRAUserCanCreateNamespace(rb.T(), standardClient, adminProject, role)
	})
	rb.Run("Validating if "+role.String()+" can lists all namespaces in a cluster.", func() {
		verifyRAUserCanListNamespace(rb.T(), rb.client, standardClient, rb.cluster.ID, role)
	})
	rb.Run("Validating if "+role.String()+" can delete a namespace from a project they own.", func() {
		verifyRAUserCanDeleteNamespace(rb.T(), rb.client, standardClient, adminProject, rb.cluster.ID, role)
	})
	rb.Run("Validating if member with role "+role.String()+" can add members to the cluster", func() {
		verifyRAUserCanAddClusterRoles(rb.T(), rb.client, standardClient, rb.cluster)
	})
	rb.Run("Validating if member with role "+role.String()+" can add members to the project", func() {
		if strings.Contains(role.String(), "project") {
			verifyRAUserCanAddProjectRoles(rb.T(), standardClient, adminProject, additionalUser, rbac.ProjectOwner.String(), rb.cluster.ID)
		}
	})
	rb.Run("Validating if member with role "+role.String()+" can delete a project they are not owner of ", func() {
		rbac.VerifyUserCanDeleteProject(rb.T(), standardClient, adminProject, role)
	})
	rb.Run("Validating if member with role "+role.String()+" is removed from the cluster and returns nil clusters", func() {
		if strings.Contains(role.String(), "cluster") {
			verifyRAUserCanRemoveClusterRoles(rb.T(), rb.client, user)
		}
	})
}

func (rb *RBTestSuite) TestRBACRA() {
	tests := []struct {
		name   string
		role   rbac.Role
		member string
	}{
		{"Restricted Admin", restrictedAdmin, restrictedAdmin.String()},
	}

	for _, tt := range tests {
		var newUser *management.User
		rb.Run("Validate conditions for user with role "+tt.name, func() {
			user, err := users.CreateUserWithRole(rb.client, users.UserConfig(), tt.member)
			require.NoError(rb.T(), err)
			newUser = user
			rb.T().Logf("Created user: %v", newUser.Username)
		})

		if newUser != nil {
			rb.sequentialTestRBACRA(tt.role, tt.member, newUser)
			subSession := rb.session.NewSession()
			defer subSession.Cleanup()
		}
	}
}

func (rb *RBTestSuite) TestRBACDynamicInputRA() {
	roles := map[string]string{
		"restricted-admin": restrictedAdmin.String(),
	}
	var member string
	userConfig := new(rbac.Config)
	config.LoadConfig(rbac.ConfigurationFileKey, userConfig)
	username := userConfig.Username
	userByName, err := users.GetUserIDByName(rb.client, username)
	require.NoError(rb.T(), err)
	user, err := rb.client.Management.User.ByID(userByName)
	require.NoError(rb.T(), err)

	user.Password = userConfig.Password

	role := userConfig.Role
	if userConfig.Role == "" {
		rb.T().Skip()
	} else {
		val, ok := roles[role.String()]
		if !ok {
			rb.FailNow("Incorrect usage of roles. Please go through the readme for correct role configurations")
		}
		role = rbac.Role(val)
	}

	member = restrictedAdmin.String()

	rb.sequentialTestRBACRA(role, member, user)

}

func TestRARBACTestSuite(t *testing.T) {
	suite.Run(t, new(RBTestSuite))
}

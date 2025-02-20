//go:build (validation || infra.any || cluster.any || stress) && !sanity && !extended && (2.8 || 2.9 || 2.10)

package psa

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/namespaces"
	psadeploy "github.com/rancher/tests/actions/psact"
	rbac "github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	restrictedAdmin rbac.Role = "restricted-admin"
)

func (rb *PSATestSuite) ValidatePSARA(role string, customRole bool) {
	labels := map[string]string{
		psaWarn:    pssPrivilegedPolicy,
		psaEnforce: pssPrivilegedPolicy,
		psaAudit:   pssPrivilegedPolicy,
	}

	rb.T().Logf("Validate updating the PSA labels as %v", role)

	updateNS, err := getAndConvertNamespace(rb.adminNamespace, rb.steveAdminClient)
	require.NoError(rb.T(), err)
	updateNS.Labels = labels

	response, err := rb.steveNonAdminClient.SteveType(namespaces.NamespaceSteveType).Update(rb.adminNamespace, updateNS)

	require.NoError(rb.T(), err)
	expectedLabels := getPSALabels(response, labels)
	assert.Equal(rb.T(), labels, expectedLabels)

	rb.T().Logf("Validate deletion of the PSA labels as %v", role)

	deletePSALabels(labels)

	deleteLabelsNS, err := getAndConvertNamespace(rb.adminNamespace, rb.steveAdminClient)
	require.NoError(rb.T(), err)
	deleteLabelsNS.Labels = labels

	_, err = rb.steveNonAdminClient.SteveType(namespaces.NamespaceSteveType).Update(rb.adminNamespace, deleteLabelsNS)

	require.NoError(rb.T(), err)
	expectedLabels = getPSALabels(response, labels)
	assert.Equal(rb.T(), 0, len(expectedLabels))
	if !customRole {
		_, err = createDeploymentAndWait(rb.steveNonAdminClient, containerName, containerImage, rb.adminNamespace.Name)
		require.NoError(rb.T(), err)
	}

	rb.T().Logf("Validate creation of new namespace with PSA labels as %v", role)

	labels = map[string]string{
		psaWarn:    pssBaselinePolicy,
		psaEnforce: pssBaselinePolicy,
		psaAudit:   pssBaselinePolicy,
	}
	namespaceName := namegen.AppendRandomString("testns-")
	namespaceCreate, err := namespaces.CreateNamespace(rb.nonAdminUserClient, namespaceName, "{}", labels, map[string]string{}, rb.adminProject)

	require.NoError(rb.T(), err)
	expectedLabels = getPSALabels(response, labels)
	assert.Equal(rb.T(), labels, expectedLabels)
	if !customRole {
		_, err = createDeploymentAndWait(rb.steveNonAdminClient, containerName, containerImage, namespaceCreate.Name)
		require.NoError(rb.T(), err)
	}

}

func (rb *PSATestSuite) ValidateEditPsactClusterRA(role string, psact string) {
	_, err := editPsactCluster(rb.nonAdminUserClient, rb.clusterName, rbac.DefaultNamespace, psact)

	require.NoError(rb.T(), err)
	err = psadeploy.CreateNginxDeployment(rb.nonAdminUserClient, rb.clusterID, psact)
	require.NoError(rb.T(), err)
}

func (rb *PSATestSuite) TestPSARA() {
	role := restrictedAdmin.String()
	var customRole bool
	if role == rbac.CreateNS.String() {
		customRole = true
	}
	createProjectAsAdmin, err := createProject(rb.client, rb.cluster.ID)
	rb.adminProject = createProjectAsAdmin
	require.NoError(rb.T(), err)

	steveAdminClient, err := rb.client.Steve.ProxyDownstream(rb.cluster.ID)
	require.NoError(rb.T(), err)
	rb.steveAdminClient = steveAdminClient
	namespaceName := namegen.AppendRandomString("testns-")
	labels := map[string]string{
		psaWarn:    pssRestrictedPolicy,
		psaEnforce: pssRestrictedPolicy,
		psaAudit:   pssRestrictedPolicy,
	}
	adminNamespace, err := namespaces.CreateNamespace(rb.client, namespaceName+"-admin", "{}", labels, map[string]string{}, rb.adminProject)
	require.NoError(rb.T(), err)
	expectedPSALabels := getPSALabels(adminNamespace, labels)
	assert.Equal(rb.T(), labels, expectedPSALabels)
	rb.adminNamespace = adminNamespace
	_, err = createDeploymentAndWait(rb.steveAdminClient, containerName, containerImage, rb.adminNamespace.Name)
	require.Error(rb.T(), err)

	rb.Run("Create a user with global role "+role, func() {
		var userRole string
		userRole = role

		newUser, err := users.CreateUserWithRole(rb.client, users.UserConfig(), userRole)

		require.NoError(rb.T(), err)
		rb.nonAdminUser = newUser
		rb.T().Logf("Created user: %v", rb.nonAdminUser.Username)
		rb.nonAdminUserClient, err = rb.client.AsUser(newUser)
		require.NoError(rb.T(), err)

		subSession := rb.session.NewSession()
		defer subSession.Cleanup()

		log.Info("Adding user as " + role + " to the downstream cluster.")

		steveClient, err := rb.nonAdminUserClient.Steve.ProxyDownstream(rb.cluster.ID)
		require.NoError(rb.T(), err)
		rb.steveNonAdminClient = steveClient
	})

	rb.Run("Testcase - Validate if members with roles "+role+"can add/edit/delete labesl from admin created namespace", func() {

		rb.ValidatePSA(role, customRole)
	})

	if strings.Contains(role, "cluster") {
		rb.Run("Additional testcase - Validate if members with roles "+role+"can add/edit/delete labels from admin created namespace", func() {
			rb.ValidateAdditionalPSA(role)
		})
	}

	if strings.Contains(role, "project") || role == rbac.CreateNS.String() {
		rb.Run("Additional testcase - Validate if "+role+" with an additional role update-psa can add/edit/delete labels from admin created namespace", func() {
			err := users.AddClusterRoleToUser(rb.client, rb.cluster, rb.nonAdminUser, rb.psaRole.ID, nil)
			require.NoError(rb.T(), err)
			rb.ValidatePSA(psaRole, customRole)
		})
	}
}

func (rb *PSATestSuite) TestPsactRBACRA() {
	tests := []struct {
		name   string
		role   string
		member string
	}{
		{"Restricted Admin", restrictedAdmin.String(), restrictedAdmin.String()},
	}
	for _, tt := range tests {
		rb.Run("Set up User with Cluster Role "+tt.name, func() {
			newUser, err := users.CreateUserWithRole(rb.client, users.UserConfig(), tt.member)
			require.NoError(rb.T(), err)
			rb.nonAdminUser = newUser
			rb.T().Logf("Created user: %v", rb.nonAdminUser.Username)
			rb.nonAdminUserClient, err = rb.client.AsUser(newUser)
			require.NoError(rb.T(), err)

			subSession := rb.session.NewSession()
			defer subSession.Cleanup()

			createProjectAsAdmin, err := createProject(rb.client, rb.cluster.ID)
			rb.adminProject = createProjectAsAdmin
			require.NoError(rb.T(), err)
		})
		rb.Run("Adding user as "+tt.name+" to the downstream cluster.", func() {
			if tt.member == rbac.StandardUser.String() {
				if strings.Contains(tt.role, "project") || tt.role == rbac.ReadOnly.String() {
					err := users.AddProjectMember(rb.client, rb.adminProject, rb.nonAdminUser, tt.role, nil)
					require.NoError(rb.T(), err)
				} else {
					err := users.AddClusterRoleToUser(rb.client, rb.cluster, rb.nonAdminUser, tt.role, nil)
					require.NoError(rb.T(), err)
				}
			}
			relogin, err := rb.nonAdminUserClient.ReLogin()
			require.NoError(rb.T(), err)
			rb.nonAdminUserClient = relogin
		})

		rb.T().Logf("Starting validations for %v", tt.role)
		rb.Run("Test case - Edit cluster as a "+tt.name+" and disable psact.", func() {
			rb.ValidateEditPsactCluster(tt.role, "")
		})
		rb.Run("Test case - Edit cluster as a "+tt.name+" and set psact to rancher-restricted.", func() {
			rb.ValidateEditPsactCluster(tt.role, "rancher-restricted")
		})
	}
}

func TestRBACPSARATestSuite(t *testing.T) {
	suite.Run(t, new(PSATestSuite))
}

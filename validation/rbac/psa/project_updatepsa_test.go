//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

package psa

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ProjectUpdatePsaTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (pu *ProjectUpdatePsaTestSuite) TearDownSuite() {
	pu.session.Cleanup()
}

func (pu *ProjectUpdatePsaTestSuite) SetupSuite() {
	pu.session = session.NewSession()
	client, err := rancher.NewClient("", pu.session)
	require.NoError(pu.T(), err)

	pu.client = client

	log.Info("Getting cluster name from the config file and append cluster details in pu")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(pu.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(pu.client, clusterName)
	require.NoError(pu.T(), err, "Error getting cluster ID")
	pu.cluster, err = pu.client.Management.Cluster.ByID(clusterID)
	require.NoError(pu.T(), err)
}

func (pu *ProjectUpdatePsaTestSuite) TestCreateNamespaceWithPsaLabelsWithoutUpdatePsa() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.Admin, rbac.Admin.String()},
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		pu.Run("Validate creation of a namespace with PSA labels without updatepsa permission for user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(pu.client, pu.cluster.ID)
			assert.NoError(pu.T(), err)

			var userClient *rancher.Client
			if tt.role == rbac.Admin {
				userClient = pu.client
			} else {
				log.Infof("Creating a standard user and adding the user to a cluster/project role %s", tt.role.String())
				newUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(pu.client, tt.member, tt.role.String(), pu.cluster, adminProject)
				assert.NoError(pu.T(), err)
				pu.T().Logf("Created user: %v", newUser.Username)
				userClient = standardUserClient
			}

			log.Infof("As %v, trying to create a namespace with PSA labels in project %v", tt.role.String(), adminProject.Name)
			psaLabels := generatePSALabels()

			createdNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, pu.cluster.ID, adminProject.Name, psaLabels)
			switch tt.role {
			case rbac.Admin, rbac.ClusterOwner:
				assert.NoError(pu.T(), err)
				actualLabels := getPSALabelsFromNamespace(createdNamespace)
				assert.Equal(pu.T(), actualLabels, psaLabels)
			case rbac.ProjectOwner, rbac.ProjectMember:
				assert.Error(pu.T(), err)
				expectedMsg := `admission webhook "rancher.cattle.io.namespaces.create-non-kubesystem" denied the request: Unauthorized`
				assert.True(pu.T(), strings.Contains(err.Error(), expectedMsg), "Expected: %v, but got: %v", expectedMsg, err.Error())
			}
		})
	}
}

func (pu *ProjectUpdatePsaTestSuite) TestCreateNamespaceWithPsaLabelsWithUpdatePsa() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		pu.Run("Validate creation of a namespace with PSA labels with updatepsa permission for user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(pu.client, pu.cluster.ID)
			assert.NoError(pu.T(), err)

			log.Infof("Creating a standard user and adding the user to a cluster/project role %s", tt.role.String())
			newUser, userClient, err := rbac.AddUserWithRoleToCluster(pu.client, tt.member, tt.role.String(), pu.cluster, adminProject)
			assert.NoError(pu.T(), err)
			pu.T().Logf("Created user: %v", newUser.Username)

			log.Infof("Granting 'updatepsa' permission to user %v", newUser.Username)
			customRoleTemplate, err := createUpdatePSARoleTemplate(pu.client)
			assert.NoError(pu.T(), err)
			_, err = rbacapi.CreateProjectRoleTemplateBinding(pu.client, newUser, adminProject, customRoleTemplate.Name)
			assert.NoError(pu.T(), err)

			log.Infof("As a %v, creating a namespace with PSA labels in the project %v", tt.role.String(), adminProject.Name)
			psaLabels := generatePSALabels()

			createdNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, pu.cluster.ID, adminProject.Name, psaLabels)
			assert.NoError(pu.T(), err, "Expected namespace creation to succeed for role %s", tt.role.String())
			actualLabels := getPSALabelsFromNamespace(createdNamespace)
			assert.Equal(pu.T(), actualLabels, psaLabels)
		})
	}
}

func (pu *ProjectUpdatePsaTestSuite) TestUpdateNamespaceWithPsaLabelsWithoutUpdatePsa() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.Admin, rbac.Admin.String()},
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		pu.Run("Validate update of PSA labels without updatepsa permission for user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace with PSA labels.")
			adminProject, createdNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(pu.client, pu.cluster.ID)
			assert.NoError(pu.T(), err)

			var userClient *rancher.Client
			if tt.role == rbac.Admin {
				userClient = pu.client
			} else {
				log.Infof("Creating a standard user and adding the user to a cluster/project role %s", tt.role.String())
				newUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(pu.client, tt.member, tt.role.String(), pu.cluster, adminProject)
				assert.NoError(pu.T(), err)
				pu.T().Logf("Created user: %v", newUser.Username)
				userClient = standardUserClient
			}

			log.Infof("As %v, updating PSA labels in namespace %v", tt.role.String(), createdNamespace.Name)
			psaLabels := generatePSALabels()

			ctx, err := clusterapi.GetClusterWranglerContext(userClient, pu.cluster.ID)
			assert.NoError(pu.T(), err)
			currentNamespace, err := namespaceapi.GetNamespaceByName(pu.client, pu.cluster.ID, createdNamespace.Name)
			assert.NoError(pu.T(), err)
			currentNamespace.ObjectMeta.Labels = psaLabels
			updatedNamespace, err := ctx.Core.Namespace().Update(currentNamespace)
			switch tt.role {
			case rbac.Admin, rbac.ClusterOwner:
				assert.NoError(pu.T(), err)
				actualLabels := getPSALabelsFromNamespace(updatedNamespace)
				assert.Equal(pu.T(), actualLabels, psaLabels)
			case rbac.ProjectOwner, rbac.ProjectMember:
				assert.Error(pu.T(), err)
				expectedMsg := `admission webhook "rancher.cattle.io.namespaces" denied the request: Unauthorized`
				assert.True(pu.T(), strings.Contains(err.Error(), expectedMsg), "Expected: %v, but got: %v", expectedMsg, err.Error())
			}
		})
	}
}

func (pu *ProjectUpdatePsaTestSuite) TestUpdateNamespaceWithPsaLabelsWithUpdatePsa() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		pu.Run("Validate update of PSA labels with updatepsa permission for user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace with PSA labels.")
			adminProject, createdNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(pu.client, pu.cluster.ID)
			assert.NoError(pu.T(), err)

			log.Infof("Creating a standard user and adding the user to a cluster/project role %s", tt.role.String())
			newUser, userClient, err := rbac.AddUserWithRoleToCluster(pu.client, tt.member, tt.role.String(), pu.cluster, adminProject)
			assert.NoError(pu.T(), err)
			pu.T().Logf("Created user: %v", newUser.Username)

			log.Infof("Granting 'updatepsa' permission to user %v", newUser.Username)
			customRoleTemplate, err := createUpdatePSARoleTemplate(pu.client)
			assert.NoError(pu.T(), err)
			_, err = rbacapi.CreateProjectRoleTemplateBinding(pu.client, newUser, adminProject, customRoleTemplate.Name)
			assert.NoError(pu.T(), err)

			log.Infof("As %v, updating PSA labels in namespace %v", tt.role.String(), createdNamespace.Name)
			psaLabels := generatePSALabels()

			ctx, err := clusterapi.GetClusterWranglerContext(userClient, pu.cluster.ID)
			assert.NoError(pu.T(), err)
			currentNamespace, err := namespaceapi.GetNamespaceByName(pu.client, pu.cluster.ID, createdNamespace.Name)
			assert.NoError(pu.T(), err)
			currentNamespace.ObjectMeta.Labels = psaLabels
			updatedNamespace, err := ctx.Core.Namespace().Update(currentNamespace)
			assert.NoError(pu.T(), err)
			actualLabels := getPSALabelsFromNamespace(updatedNamespace)
			assert.Equal(pu.T(), actualLabels, psaLabels)
		})
	}
}

func (pu *ProjectUpdatePsaTestSuite) TestCreateNamespaceWithPsaLabelsAsStandardUser() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a project as admin.")
	adminProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(pu.client, pu.cluster.ID)
	require.NoError(pu.T(), err)

	customRoleTemplate2, err := createUpdatePSARoleTemplate(pu.client)
	require.NoError(pu.T(), err)

	log.Infof("Creating user")
	newUser, userClient, err := rbac.SetupUser(pu.client, rbac.StandardUser.String())
	require.NoError(pu.T(), err)
	log.Infof("Created user: %v", newUser.Username)

	log.Infof("Granting 'updatepsa' permission to user %v", newUser.Username)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(pu.client, newUser, adminProject, customRoleTemplate2.Name)
	require.NoError(pu.T(), err)

	log.Infof("Granting 'create namespaces' permission to user %v", newUser.Username)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(pu.client, newUser, adminProject, rbac.CreateNS.String())
	require.NoError(pu.T(), err)

	log.Infof("As user %v, create a namespace with PSA labels in project %v", newUser.Username, adminProject.Name)
	psaLabels := generatePSALabels()
	createdNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, pu.cluster.ID, adminProject.Name, psaLabels)
	require.NoError(pu.T(), err)

	actualLabels := getPSALabelsFromNamespace(createdNamespace)
	require.Equal(pu.T(), actualLabels, psaLabels)
}

func (pu *ProjectUpdatePsaTestSuite) TestVerifyCreateNamespaceWithPsaLabelsWithMultipleUsers() {
	subSession := pu.session.NewSession()
	defer subSession.Cleanup()

	const userCount = 5

	log.Info("Creating a project as admin.")
	adminProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(pu.client, pu.cluster.ID)
	require.NoError(pu.T(), err)

	customRoleTemplate2, err := createUpdatePSARoleTemplate(pu.client)
	require.NoError(pu.T(), err)

	for i := 0; i < userCount; i++ {
		log.Infof("Creating user")
		newUser, userClient, err := rbac.SetupUser(pu.client, rbac.StandardUser.String())
		require.NoError(pu.T(), err)
		log.Infof("Created user: %v", newUser.Username)

		log.Infof("Granting 'updatepsa' permission to user %v", newUser.Username)
		_, err = rbacapi.CreateProjectRoleTemplateBinding(pu.client, newUser, adminProject, customRoleTemplate2.Name)
		require.NoError(pu.T(), err)

		log.Infof("Granting 'create namespaces' permission to user %v", newUser.Username)
		_, err = rbacapi.CreateProjectRoleTemplateBinding(pu.client, newUser, adminProject, rbac.CreateNS.String())
		require.NoError(pu.T(), err)

		log.Infof("As user %v, create a namespace with PSA labels in project %v", newUser.Username, adminProject.Name)
		psaLabels := generatePSALabels()
		createdNamespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, pu.cluster.ID, adminProject.Name, psaLabels)
		require.NoError(pu.T(), err)

		actualLabels := getPSALabelsFromNamespace(createdNamespace)
		require.Equal(pu.T(), actualLabels, psaLabels)
	}
}

func TestProjectUpdatePsaTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectUpdatePsaTestSuite))
}

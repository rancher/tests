//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package projects

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RbacProjectTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rbp *RbacProjectTestSuite) TearDownSuite() {
	rbp.session.Cleanup()
}

func (rbp *RbacProjectTestSuite) SetupSuite() {
	rbp.session = session.NewSession()

	client, err := rancher.NewClient("", rbp.session)
	assert.NoError(rbp.T(), err)
	rbp.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rb")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rbp.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rbp.client, clusterName)
	require.NoError(rbp.T(), err, "Error getting cluster ID")
	rbp.cluster, err = rbp.client.Management.Cluster.ByID(clusterID)
	assert.NoError(rbp.T(), err)
}

func (rbp *RbacProjectTestSuite) TestCreateProject() {
	subSession := rbp.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rbp.Run("Validate project creation for user with role "+tt.role.String(), func() {
			log.Infof("Create a standard user and add the user to the downstream cluster as %v", tt.role.String())
			standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(rbp.client, tt.member, tt.role.String(), rbp.cluster, nil)
			assert.NoError(rbp.T(), err)

			log.Infof("As a %v, create a project in the downstream cluster.", tt.role.String())
			projectTemplate := projectapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			_, err = projectapi.CreateProjectWithTemplate(standardUserClient, rbp.cluster.ID, projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")
		})
	}
}

func (rbp *RbacProjectTestSuite) TestListProject() {
	subSession := rbp.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rbp.Run("Validate listing projects for user with role "+tt.role.String(), func() {
			log.Infof("Create a standard user and add the user to the downstream cluster as %v", tt.role.String())
			standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(rbp.client, tt.member, tt.role.String(), rbp.cluster, nil)
			assert.NoError(rbp.T(), err)

			log.Infof("As a %v, create a project in the downstream cluster.", tt.role.String())
			projectTemplate := projectapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			createdProject, err := projectapi.CreateProjectWithTemplate(standardUserClient, rbp.cluster.ID, projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")

			log.Infof("As a %v, get the project in the downstream cluster.", tt.role.String())
			err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
			assert.NoError(rbp.T(), err)
			projectObj, err := projectapi.GetProjectByName(standardUserClient, rbp.cluster.ID, createdProject.Name)
			assert.NoError(rbp.T(), err, "Failed to get project.")
			assert.NotNil(rbp.T(), projectObj, "Expected project to be not nil.")
		})
	}
}

func (rbp *RbacProjectTestSuite) TestUpdateProject() {
	subSession := rbp.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rbp.Run("Validate updating project by user with role "+tt.role.String(), func() {
			log.Infof("Create a standard user and add the user to the downstream cluster as %v", tt.role.String())
			standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(rbp.client, tt.member, tt.role.String(), rbp.cluster, nil)
			assert.NoError(rbp.T(), err)

			log.Infof("As a %v, create a project in the downstream cluster.", tt.role.String())
			projectTemplate := projectapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			createdProject, err := projectapi.CreateProjectWithTemplate(standardUserClient, rbp.cluster.ID, projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")

			log.Infof("As a %v, get the project in the downstream cluster.", tt.role.String())
			err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
			assert.NoError(rbp.T(), err)
			currentProject, err := projectapi.GetProjectByName(standardUserClient, rbp.cluster.ID, createdProject.Name)
			assert.NoError(rbp.T(), err, "Failed to get project.")
			assert.NotNil(rbp.T(), currentProject, "Expected project to be not nil.")

			log.Infof("As a %v, verify that the project can be updated by adding a label.", tt.role.String())
			if currentProject.Labels == nil {
				currentProject.Labels = make(map[string]string)
			}
			currentProject.Labels["hello"] = "world"
			_, err = projectapi.UpdateProject(standardUserClient, currentProject)
			assert.NoError(rbp.T(), err, "Failed to update project.")

			updatedProject, err := projectapi.GetProjectByName(standardUserClient, rbp.cluster.ID, currentProject.Name)
			assert.NoError(rbp.T(), err)
			assert.Equal(rbp.T(), "world", updatedProject.Labels["hello"], "Label was not added to the project.")
		})
	}
}

func (rbp *RbacProjectTestSuite) TestDeleteProject() {
	subSession := rbp.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rbp.Run("Validate project deletion by user with role "+tt.role.String(), func() {
			log.Infof("Create a standard user and add the user to the downstream cluster as %v", tt.role.String())
			standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(rbp.client, tt.member, tt.role.String(), rbp.cluster, nil)
			assert.NoError(rbp.T(), err)

			log.Infof("As a %v, create a project in the downstream cluster.", tt.role.String())
			projectTemplate := projectapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			createdProject, err := projectapi.CreateProjectWithTemplate(standardUserClient, rbp.cluster.ID, projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")

			log.Infof("As a %v, get the project in the downstream cluster.", tt.role.String())
			err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
			assert.NoError(rbp.T(), err)
			currentProject, err := projectapi.GetProjectByName(standardUserClient, createdProject.Namespace, createdProject.Name)
			assert.NoError(rbp.T(), err, "Failed to get project.")
			assert.NotNil(rbp.T(), currentProject, "Expected project to be not nil.")

			log.Infof("As a %v, delete the project.", tt.role.String())
			err = projectapi.DeleteProject(standardUserClient, rbp.cluster.ID, createdProject.Name, true)
			assert.NoError(rbp.T(), err, tt.role.String()+" should be able to delete project")
		})
	}
}

func (rbp *RbacProjectTestSuite) TestCrossClusterResourceIsolation() {
	subSession := rbp.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a project and associated namespace in the local cluster")
	firstProject, firstNamespace, err := projectapi.CreateProjectAndNamespace(rbp.client, extclusterapi.LocalCluster)
	require.NoError(rbp.T(), err)

	log.Info("Creating a standard user and assigning the cluster-member role in the downstream cluster")
	standardUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(rbp.client, rbac.StandardUser.String(), rbac.ClusterMember.String(), rbp.cluster, nil)
	require.NoError(rbp.T(), err, "Failed to add the user as a cluster owner to the downstream cluster")

	log.Infof("As %s, creating a project with the same name in the downstream cluster", rbac.ClusterMember.String())
	projectTemplate := projectapi.NewProjectTemplate(rbp.cluster.ID)
	projectTemplate.Name = firstProject.Name
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": standardUser.ID,
	}
	createdProject, err := projectapi.CreateProjectWithTemplate(standardUserClient, rbp.cluster.ID, projectTemplate)
	require.NoError(rbp.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(rbp.T(), err)

	secondNamespace, err := namespaceapi.CreateNamespace(standardUserClient, rbp.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns-"), "", nil, nil)
	require.NoError(rbp.T(), err)

	log.Infof("As %s, attempting to create a PRTB referencing the project in the local cluster", rbac.ClusterMember.String())
	prtb := rbacapi.NewProjectRoleTemplateBindingTemplate()
	prtb.ProjectName = fmt.Sprintf("%s:%s", extclusterapi.LocalCluster, firstProject.Name)
	prtb.Name = namegen.AppendRandomString("prtb-")
	prtb.RoleTemplateName = rbac.ProjectOwner.String()
	prtb.UserPrincipalName = "local://" + standardUser.ID
	prtb.Namespace = firstProject.Name
	if firstProject.Status.BackingNamespace != "" {
		prtb.Namespace = firstProject.Status.BackingNamespace
	}
	_, err = standardUserClient.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(&prtb)
	require.Error(rbp.T(), err, "Expected failure: user should not be able to create PRTB referencing the project in the local cluster")

	log.Infof("As %s, verifying that the user cannot access the namespace in the local cluster", rbac.ClusterMember.String())
	_, err = standardUserClient.WranglerContext.Core.Namespace().Get(firstNamespace.Name, metav1.GetOptions{})
	require.Error(rbp.T(), err, "User should not have access to the namespace in the local cluster")

	log.Infof("As %s, verifying that the user can access the namespace in the downstream cluster", rbac.ClusterMember.String())
	_, err = extnamespaceapi.GetNamespaceByName(standardUserClient, rbp.cluster.ID, secondNamespace.Name)
	require.NoError(rbp.T(), err, "User should be able to access the namespace in the downstream cluster")
}

func TestRbacProjectTestSuite(t *testing.T) {
	suite.Run(t, new(RbacProjectTestSuite))
}

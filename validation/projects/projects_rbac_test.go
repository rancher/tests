//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package projects

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	projectsapi "github.com/rancher/tests/actions/kubeapi/projects"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
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
			projectTemplate := projectsapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			_, err = standardUserClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
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
			projectTemplate := projectsapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			createdProject, err := standardUserClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")

			log.Infof("As a %v, get the project in the downstream cluster.", tt.role.String())
			err = projects.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
			assert.NoError(rbp.T(), err)
			projectObj, err := standardUserClient.WranglerContext.Mgmt.Project().Get(rbp.cluster.ID, createdProject.Name, metav1.GetOptions{})
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
			projectTemplate := projectsapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			createdProject, err := standardUserClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")

			log.Infof("As a %v, get the project in the downstream cluster.", tt.role.String())
			err = projects.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
			assert.NoError(rbp.T(), err)
			currentProject, err := standardUserClient.WranglerContext.Mgmt.Project().Get(rbp.cluster.ID, createdProject.Name, metav1.GetOptions{})
			assert.NoError(rbp.T(), err, "Failed to get project.")
			assert.NotNil(rbp.T(), currentProject, "Expected project to be not nil.")

			log.Infof("As a %v, verify that the project can be updated by adding a label.", tt.role.String())
			if currentProject.Labels == nil {
				currentProject.Labels = make(map[string]string)
			}
			currentProject.Labels["hello"] = "world"
			_, err = standardUserClient.WranglerContext.Mgmt.Project().Update(currentProject)
			assert.NoError(rbp.T(), err, "Failed to update project.")

			updatedProject, err := standardUserClient.WranglerContext.Mgmt.Project().Get(rbp.cluster.ID, currentProject.Name, metav1.GetOptions{})
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
			projectTemplate := projectsapi.NewProjectTemplate(rbp.cluster.ID)
			if tt.role.String() == rbac.ClusterMember.String() {
				projectTemplate.Annotations = map[string]string{
					"field.cattle.io/creatorId": standardUser.ID,
				}
			}
			createdProject, err := standardUserClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
			assert.NoError(rbp.T(), err, "failed to create project")

			log.Infof("As a %v, get the project in the downstream cluster.", tt.role.String())
			err = projects.WaitForProjectFinalizerToUpdate(standardUserClient, createdProject.Name, createdProject.Namespace, 2)
			assert.NoError(rbp.T(), err)
			currentProject, err := standardUserClient.WranglerContext.Mgmt.Project().Get(rbp.cluster.ID, createdProject.Name, metav1.GetOptions{})
			assert.NoError(rbp.T(), err, "Failed to get project.")
			assert.NotNil(rbp.T(), currentProject, "Expected project to be not nil.")

			log.Infof("As a %v, delete the project.", tt.role.String())
			err = standardUserClient.WranglerContext.Mgmt.Project().Delete(rbp.cluster.ID, createdProject.Name, &metav1.DeleteOptions{})
			assert.NoError(rbp.T(), err, "Failed to delete project")
			err = kwait.Poll(defaults.FiveHundredMillisecondTimeout, defaults.TenSecondTimeout, func() (bool, error) {
				_, pollErr := standardUserClient.WranglerContext.Mgmt.Project().Get(rbp.cluster.ID, createdProject.Name, metav1.GetOptions{})
				if pollErr != nil {
					return true, pollErr
				}

				return false, nil
			})
			assert.Error(rbp.T(), err)
		})
	}
}

func TestRbacProjectTestSuite(t *testing.T) {
	suite.Run(t, new(RbacProjectTestSuite))
}

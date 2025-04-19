//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package clusterandprojectroles

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/projects"
	rbac "github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ProjectRolesTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (pr *ProjectRolesTestSuite) TearDownSuite() {
	pr.session.Cleanup()
}

func (pr *ProjectRolesTestSuite) SetupSuite() {
	pr.session = session.NewSession()
	client, err := rancher.NewClient("", pr.session)
	require.NoError(pr.T(), err)

	pr.client = client

	log.Info("Getting cluster name from the config file and append cluster details in pr")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(pr.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(pr.client, clusterName)
	require.NoError(pr.T(), err, "Error getting cluster ID")
	pr.cluster, err = pr.client.Management.Cluster.ByID(clusterID)
	require.NoError(pr.T(), err)
}

func (pr *ProjectRolesTestSuite) testSetupUserAndProject() (*rancher.Client, *v3.Project) {
	pr.T().Log("Set up User with cluster role for additional rbac test cases.")
	newUser, standardUserClient, err := rbac.SetupUser(pr.client, rbac.StandardUser.String())
	require.NoError(pr.T(), err)

	adminProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(pr.client, pr.cluster.ID)
	require.NoError(pr.T(), err)

	log.Info("Adding a standard user as project owner in the admin project")
	_, errUserRole := rbac.CreateProjectRoleTemplateBinding(pr.client, newUser, adminProject, rbac.ProjectOwner.String())
	require.NoError(pr.T(), errUserRole)
	standardUserClient, err = standardUserClient.ReLogin()
	require.NoError(pr.T(), err)

	return standardUserClient, adminProject
}

func (pr *ProjectRolesTestSuite) TestProjectOwnerAddsAndRemovesOtherProjectOwners() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, adminProject := pr.testSetupUserAndProject()

	additionalUser, additionalUserClient, err := rbac.SetupUser(pr.client, rbac.StandardUser.String())
	require.NoError(pr.T(), err)

	createdPrtb, errUserRole := rbac.CreateProjectRoleTemplateBinding(standardUserClient, additionalUser, adminProject, rbac.ProjectOwner.String())
	require.NoError(pr.T(), errUserRole)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(pr.T(), err)

	userGetProject, err := additionalUserClient.WranglerContext.Mgmt.Project().Get(pr.cluster.ID, adminProject.Name, metav1.GetOptions{})
	require.NoError(pr.T(), err)
	require.Equal(pr.T(), userGetProject.Name, adminProject.Name)

	errRemoveMember := standardUserClient.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(createdPrtb.Namespace, createdPrtb.Name, &metav1.DeleteOptions{})
	require.NoError(pr.T(), errRemoveMember)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(pr.T(), err)

	_, err = additionalUserClient.WranglerContext.Mgmt.Project().Get(pr.cluster.ID, adminProject.Name, metav1.GetOptions{})
	require.Error(pr.T(), err)
	require.True(pr.T(), apierrors.IsForbidden(err))
}

func (pr *ProjectRolesTestSuite) TestManageProjectUserRoleCannotAddProjectOwner() {
	subSession := pr.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, adminProject := pr.testSetupUserAndProject()
	additionalUser, additionalUserClient, err := rbac.SetupUser(pr.client, rbac.StandardUser.String())
	require.NoError(pr.T(), err)

	_, errUserRole := rbac.CreateProjectRoleTemplateBinding(standardUserClient, additionalUser, adminProject, rbac.CustomManageProjectMember.String())
	require.NoError(pr.T(), errUserRole)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(pr.T(), err)

	addNewUserAsProjectOwner, addNewUserAsPOClient, err := rbac.SetupUser(pr.client, rbac.StandardUser.String())
	require.NoError(pr.T(), err)

	_, errUserRole2 := rbac.CreateProjectRoleTemplateBinding(additionalUserClient, addNewUserAsProjectOwner, adminProject, rbac.ProjectOwner.String())
	require.Error(pr.T(), errUserRole2)
	addNewUserAsPOClient, err = addNewUserAsPOClient.ReLogin()
	require.NoError(pr.T(), err)

	_, err = addNewUserAsPOClient.WranglerContext.Mgmt.Project().Get(pr.cluster.ID, adminProject.Name, metav1.GetOptions{})
	require.Error(pr.T(), err)
	require.True(pr.T(), apierrors.IsForbidden(err))
}

func TestProjectRolesTestSuite(t *testing.T) {
	suite.Run(t, new(ProjectRolesTestSuite))
}

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
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ClusterRoleTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rb *ClusterRoleTestSuite) TearDownSuite() {
	rb.session.Cleanup()
}

func (rb *ClusterRoleTestSuite) SetupSuite() {
	rb.session = session.NewSession()

	client, err := rancher.NewClient("", rb.session)
	require.NoError(rb.T(), err)

	rb.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rb")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rb.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rb.client, clusterName)
	require.NoError(rb.T(), err, "Error getting cluster ID")
	rb.cluster, err = rb.client.Management.Cluster.ByID(clusterID)
	require.NoError(rb.T(), err)
}

func (rb *ClusterRoleTestSuite) testSetupUserAndProject() (*rancher.Client, *v3.Project) {
	rb.T().Log("Set up User with cluster role for additional rbac test cases " + rbac.ClusterOwner)
	newUser, standardUserClient, err := rbac.SetupUser(rb.client, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	log.Info("Adding a standard user to the downstream cluster as cluster owner")
	_, errUserRole := rbac.CreateClusterRoleTemplateBinding(rb.client, rb.cluster.ID, newUser, rbac.ClusterOwner.String())
	require.NoError(rb.T(), errUserRole)
	standardUserClient, err = standardUserClient.ReLogin()
	require.NoError(rb.T(), err)

	createProjectAsClusterOwner, _, err := projects.CreateProjectAndNamespaceUsingWrangler(standardUserClient, rb.cluster.ID)
	require.NoError(rb.T(), err)

	_, errProjectOwnerRole := rbac.CreateProjectRoleTemplateBinding(rb.client, newUser, createProjectAsClusterOwner, rbac.CustomManageProjectMember.String())
	require.NoError(rb.T(), errProjectOwnerRole)
	standardUserClient, err = standardUserClient.ReLogin()
	require.NoError(rb.T(), err)

	return standardUserClient, createProjectAsClusterOwner
}

func (rb *ClusterRoleTestSuite) TestClusterOwnerAddsUserAsProjectOwner() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, clusterOwnerProject := rb.testSetupUserAndProject()

	additionalUser, additionalUserClient, err := rbac.SetupUser(rb.client, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	prtb, err := rbac.CreateProjectRoleTemplateBinding(standardUserClient, additionalUser, clusterOwnerProject, rbac.ProjectOwner.String())
	require.NoError(rb.T(), err)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(rb.T(), err)

	projectAdditionalUser, err := additionalUserClient.WranglerContext.Mgmt.Project().Get(rb.cluster.ID, clusterOwnerProject.Name, metav1.GetOptions{})
	require.NoError(rb.T(), err)
	require.Equal(rb.T(), clusterOwnerProject.Name, projectAdditionalUser.Name)

	err = standardUserClient.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(clusterOwnerProject.Name, prtb.Name, &metav1.DeleteOptions{})
	require.NoError(rb.T(), err)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(rb.T(), err)

	_, err = additionalUserClient.WranglerContext.Mgmt.Project().Get(rb.cluster.ID, clusterOwnerProject.Name, metav1.GetOptions{})
	require.Error(rb.T(), err)
	require.True(rb.T(), apierrors.IsForbidden(err))
}

func (rb *ClusterRoleTestSuite) TestClusterOwnerAddsUserAsClusterOwner() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, _ := rb.testSetupUserAndProject()

	additionalUser, additionalUserClient, err := rbac.SetupUser(rb.client, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	crtb, err := rbac.CreateClusterRoleTemplateBinding(standardUserClient, rb.cluster.ID, additionalUser, rbac.ClusterOwner.String())
	require.NoError(rb.T(), err)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(rb.T(), err)

	clusterList, err := additionalUserClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
	require.NoError(rb.T(), err)
	assert.Equal(rb.T(), 1, len(clusterList.Data))

	err = standardUserClient.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(rb.T(), err)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(rb.T(), err)

	clusterList, err = additionalUserClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
	require.Error(rb.T(), err)
	require.Nil(rb.T(), clusterList, "Cluster list should be nil")
}

func (rb *ClusterRoleTestSuite) TestClusterOwnerAddsClusterMemberAsProjectOwner() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	standardUserClient, clusterOwnerProject := rb.testSetupUserAndProject()

	additionalUser, additionalUserClient, err := rbac.SetupUser(rb.client, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	_, err = rbac.CreateClusterRoleTemplateBinding(standardUserClient, rb.cluster.ID, additionalUser, rbac.ClusterMember.String())
	require.NoError(rb.T(), err)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(rb.T(), err)

	clusterList, err := additionalUserClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
	require.NoError(rb.T(), err)
	assert.Equal(rb.T(), 1, len(clusterList.Data))

	_, err = rbac.CreateProjectRoleTemplateBinding(standardUserClient, additionalUser, clusterOwnerProject, rbac.ProjectOwner.String())
	require.NoError(rb.T(), err)
	additionalUserClient, err = additionalUserClient.ReLogin()
	require.NoError(rb.T(), err)

	projectAdditionalUser, err := additionalUserClient.WranglerContext.Mgmt.Project().Get(rb.cluster.ID, clusterOwnerProject.Name, metav1.GetOptions{})
	require.NoError(rb.T(), err)
	require.Equal(rb.T(), clusterOwnerProject.Name, projectAdditionalUser.Name)
}

func TestClusterRoleTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterRoleTestSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package clusterandprojectroles

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/secrets"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
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

func (rb *ClusterRoleTestSuite) testSetupUserAndProject(role string) (*management.User, *rancher.Client, *v3.Project, *corev1.Namespace) {
	rb.T().Log("Set up User with cluster role for additional rbac test cases " + rbac.ClusterOwner)
	standardUser, standardUserClient, err := rbac.SetupUser(rb.client, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	log.Infof("Adding a standard user to the downstream cluster as %s", role)
	_, errUserRole := rbac.CreateClusterRoleTemplateBinding(rb.client, rb.cluster.ID, standardUser, role)
	require.NoError(rb.T(), errUserRole)
	standardUserClient, err = standardUserClient.ReLogin()
	require.NoError(rb.T(), err)

	var userClient *rancher.Client
	if role == rbac.ClusterMember.String() {
		userClient = rb.client
	} else {
		userClient = standardUserClient
	}
	createdProject, createdNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(userClient, rb.cluster.ID)
	require.NoError(rb.T(), err)

	_, errProjectOwnerRole := rbac.CreateProjectRoleTemplateBinding(rb.client, standardUser, createdProject, rbac.CustomManageProjectMember.String())
	require.NoError(rb.T(), errProjectOwnerRole)
	standardUserClient, err = standardUserClient.ReLogin()
	require.NoError(rb.T(), err)

	return standardUser, standardUserClient, createdProject, createdNamespace
}

func (rb *ClusterRoleTestSuite) TestClusterOwnerAddsUserAsProjectOwner() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	_, standardUserClient, clusterOwnerProject, _ := rb.testSetupUserAndProject(rbac.ClusterOwner.String())

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

	_, standardUserClient, _, _ := rb.testSetupUserAndProject(rbac.ClusterOwner.String())

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

	_, standardUserClient, clusterOwnerProject, _ := rb.testSetupUserAndProject(rbac.ClusterOwner.String())

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

func (rb *ClusterRoleTestSuite) TestClusterMemberWithPrtbAccess() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	standardUser, standardUserClient, adminProject, _ := rb.testSetupUserAndProject(rbac.ClusterMember.String())

	additionalUser, _, err := rbac.SetupUser(rb.client, rbac.StandardUser.String())
	require.NoError(rb.T(), err)

	log.Infof("Creating a cluster role template with verbs *, resources projectroletemplatebindings")
	rules := []rbacv1.PolicyRule{{
		Verbs:     []string{"*"},
		APIGroups: []string{rbac.ManagementAPIGroup},
		Resources: []string{rbac.PrtbResource},
	}}
	createdRoleTemplate, err := rbac.CreateRoleTemplate(rb.client, rbac.ClusterContext, rules, nil, false, nil)
	require.NoError(rb.T(), err, "Failed to create main role template")

	log.Info("Adding the user to the downstream cluster with the cluster role template")
	_, err = rbac.CreateClusterRoleTemplateBinding(rb.client, rb.cluster.ID, standardUser, createdRoleTemplate.Name)
	require.NoError(rb.T(), err)
	_, err = rbac.CreateClusterRoleTemplateBinding(rb.client, rb.cluster.ID, standardUser, rbac.ProjectsView.String())
	require.NoError(rb.T(), err)

	log.Info("As cluster member with PRTB permissions, verifying CRUD projectroletemplatebindings")
	prtb, err := rbac.CreateProjectRoleTemplateBinding(standardUserClient, additionalUser, adminProject, rbac.PrtbView.String())
	require.NoError(rb.T(), err)

	userContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, rbac.LocalCluster)
	require.NoError(rb.T(), err)
	prtb, err = userContext.Mgmt.ProjectRoleTemplateBinding().Get(adminProject.Name, prtb.Name, metav1.GetOptions{})
	require.NoError(rb.T(), err)

	if prtb.Labels == nil {
		prtb.Labels = make(map[string]string)
	}
	prtb.Labels["updated"] = "true"
	updatedPrtb, err := userContext.Mgmt.ProjectRoleTemplateBinding().Update(prtb)
	require.NoError(rb.T(), err)

	err = userContext.Mgmt.ProjectRoleTemplateBinding().Delete(adminProject.Name, updatedPrtb.Name, &metav1.DeleteOptions{})
	require.NoError(rb.T(), err)
}

func (rb *ClusterRoleTestSuite) TestClusterMemberWithSecretAccess() {
	subSession := rb.session.NewSession()
	defer subSession.Cleanup()

	standardUser, standardUserClient, _, namespace := rb.testSetupUserAndProject(rbac.ClusterMember.String())

	log.Infof("Creating a cluster role template with verbs *, resources secrets")
	rules := []rbacv1.PolicyRule{{
		Verbs:     []string{"*"},
		APIGroups: []string{""},
		Resources: []string{rbac.SecretsResource},
	}}
	createdRoleTemplate, err := rbac.CreateRoleTemplate(rb.client, rbac.ClusterContext, rules, nil, false, nil)
	require.NoError(rb.T(), err, "Failed to create main role template")

	log.Info("Adding the user to the downstream cluster with the cluster role template")
	_, err = rbac.CreateClusterRoleTemplateBinding(rb.client, rb.cluster.ID, standardUser, createdRoleTemplate.Name)
	require.NoError(rb.T(), err)
	_, err = rbac.CreateClusterRoleTemplateBinding(rb.client, rb.cluster.ID, standardUser, rbac.ProjectsView.String())
	require.NoError(rb.T(), err)

	log.Info("As cluster member with secrets permissions, verifying CRUD secrets")
	secretData := map[string][]byte{
		"hello": []byte("world"),
	}
	createdSecret, err := secrets.CreateSecret(standardUserClient, rb.cluster.ID, namespace.Name, secretData, corev1.SecretTypeOpaque)
	require.NoError(rb.T(), err, "failed to create secret")

	userContext, err := clusterapi.GetClusterWranglerContext(standardUserClient, rb.cluster.ID)
	require.NoError(rb.T(), err)
	_, err = userContext.Core.Secret().Get(createdSecret.Namespace, createdSecret.Name, metav1.GetOptions{})
	require.NoError(rb.T(), err)

	newData := map[string][]byte{
		"foo": []byte("bar"),
	}
	updatedSecretObj := secrets.UpdateSecretData(createdSecret, newData)
	updatedSecret, err := userContext.Core.Secret().Update(updatedSecretObj)
	require.NoError(rb.T(), err)

	err = userContext.Core.Secret().Delete(updatedSecret.Namespace, updatedSecret.Name, &metav1.DeleteOptions{})
	require.NoError(rb.T(), err)
}

func TestClusterRoleTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterRoleTestSuite))
}

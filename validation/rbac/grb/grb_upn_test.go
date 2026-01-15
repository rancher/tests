//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10

package grb

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type GlobalRoleBindingUserPrincipalNameTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (upn *GlobalRoleBindingUserPrincipalNameTestSuite) TearDownSuite() {
	upn.session.Cleanup()
}

func (upn *GlobalRoleBindingUserPrincipalNameTestSuite) SetupSuite() {
	upn.session = session.NewSession()

	client, err := rancher.NewClient("", upn.session)
	require.NoError(upn.T(), err)
	upn.client = client

	log.Info("Getting cluster name from the config file and append cluster details in upn")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(upn.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(upn.client, clusterName)
	require.NoError(upn.T(), err, "Error getting cluster ID")
	upn.cluster, err = upn.client.Management.Cluster.ByID(clusterID)
	require.NoError(upn.T(), err)
}

func (upn *GlobalRoleBindingUserPrincipalNameTestSuite) TestCreateGlobalRoleBindingWithUserPrincipalName() {
	subSession := upn.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a global role that inherits cluster-member")
	globalRoleWithInheritedClusterMember, _, err := createGlobalRoleAndUser(upn.client)
	require.NoError(upn.T(), err)

	globalRoleWithInheritedClusterMember.InheritedClusterRoles = []string{rbac.ClusterMember.String()}
	_, err = rbacapi.UpdateGlobalRole(upn.client, globalRoleWithInheritedClusterMember)
	require.NoError(upn.T(), err)

	log.Info("Create a globalrolebinding with userPrincipalName set")
	customGlobalRoleBinding.Name = namegen.AppendRandomString("testgrb")
	customGlobalRoleBinding.GlobalRoleName = globalRoleWithInheritedClusterMember.Name
	_, err = upn.client.WranglerContext.Mgmt.GlobalRoleBinding().Create(&customGlobalRoleBinding)
	require.NoError(upn.T(), err)

	log.Info("Verify user in globalrolebinding userPrincipalName field is created")
	err = verifyUserByPrincipalIDExists(upn.client, upnString)
	require.NoError(upn.T(), err)
}

func (upn *GlobalRoleBindingUserPrincipalNameTestSuite) TestGlobalRoleBindingPrincipalDisplayNameAnnotation() {
	subSession := upn.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a global role that inherits cluster-member")
	globalRoleWithInheritedClusterMember, _, err := createGlobalRoleAndUser(upn.client)
	require.NoError(upn.T(), err)

	globalRoleWithInheritedClusterMember.InheritedClusterRoles = []string{rbac.ClusterMember.String()}
	_, err = rbacapi.UpdateGlobalRole(upn.client, globalRoleWithInheritedClusterMember)
	require.NoError(upn.T(), err)

	log.Info("Create a globalrolebinding with userPrincipalName and principal-display-name annotation set")
	customGlobalRoleBinding.Name = namegen.AppendRandomString("testgrb")
	customGlobalRoleBinding.GlobalRoleName = globalRoleWithInheritedClusterMember.Name
	customGlobalRoleBinding.Annotations[principalDisplayNameAnnotation] = testPrincipalDisplayName
	createdGlobalRoleBinding, err := upn.client.WranglerContext.Mgmt.GlobalRoleBinding().Create(&customGlobalRoleBinding)
	require.NoError(upn.T(), err)
	require.Equal(upn.T(), testPrincipalDisplayName, createdGlobalRoleBinding.Annotations[principalDisplayNameAnnotation])
}

func (upn *GlobalRoleBindingUserPrincipalNameTestSuite) TestGlobalRoleBindingUserPrincipalNameAndGroupPrincipalNameWebhookRejectsRequest() {
	subSession := upn.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a global role that inherits cluster-member")
	globalRoleWithInheritedClusterMember, _, err := createGlobalRoleAndUser(upn.client)
	require.NoError(upn.T(), err)

	globalRoleWithInheritedClusterMember.InheritedClusterRoles = []string{rbac.ClusterMember.String()}
	_, err = rbacapi.UpdateGlobalRole(upn.client, globalRoleWithInheritedClusterMember)
	require.NoError(upn.T(), err)

	log.Info("Attempt to create a globalrolebinding with userPrincipalName and groupPrincipalName")
	customGlobalRoleBinding.Name = namegen.AppendRandomString("testgrb-both")
	customGlobalRoleBinding.GlobalRoleName = globalRoleWithInheritedClusterMember.Name
	customGlobalRoleBinding.GroupPrincipalName = testGroupPrincipalName
	_, err = upn.client.WranglerContext.Mgmt.GlobalRoleBinding().Create(&customGlobalRoleBinding)
	require.Error(upn.T(), err)
	require.Contains(upn.T(), err.Error(), "Forbidden: bindings can not set both userName/userPrincipalName and groupPrincipalName")

	log.Info("Attempt to create a globalrolebinding with userName and groupPrincipalName")
	customGlobalRoleBinding.Name = namegen.AppendRandomString("testgrb-userboth")
	customGlobalRoleBinding.GlobalRoleName = globalRoleWithInheritedClusterMember.Name
	customGlobalRoleBinding.GroupPrincipalName = testGroupPrincipalName
	customGlobalRoleBinding.UserName = namegen.AppendRandomString("test-username")
	_, err = upn.client.WranglerContext.Mgmt.GlobalRoleBinding().Create(&customGlobalRoleBinding)
	require.Error(upn.T(), err)
	require.Contains(upn.T(), err.Error(), "Forbidden: bindings can not set both userName/userPrincipalName and groupPrincipalName")
}

func TestGlobalRolesUserPrincipalNameTestSuite(t *testing.T) {
	suite.Run(t, new(GlobalRoleBindingUserPrincipalNameTestSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package activedirectory

import (
	"fmt"
	"slices"
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ActiveDirectoryAuthProviderSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	cluster    *v3.Cluster
	adminUser  *v3.User
	authConfig *authactions.AuthConfig
}

func (a *ActiveDirectoryAuthProviderSuite) SetupSuite() {
	a.session = session.NewSession()

	client, err := rancher.NewClient("", a.session)
	require.NoError(a.T(), err, "Failed to create Rancher client")
	a.client = client

	logrus.Info("Loading auth configuration from config file")
	a.authConfig = new(authactions.AuthConfig)
	config.LoadConfig(authactions.ActiveDirectoryAuthInput, a.authConfig)
	require.NotNil(a.T(), a.authConfig, "Auth configuration is not provided")

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(a.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(a.client, clusterName)
	require.NoError(a.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	a.cluster, err = a.client.Management.Cluster.ByID(clusterID)
	require.NoError(a.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Setting up admin user credentials for Active Directory authentication")
	a.adminUser = &v3.User{
		Username: client.Auth.ActiveDirectory.Config.Users.Admin.Username,
		Password: client.Auth.ActiveDirectory.Config.Users.Admin.Password,
	}

	logrus.Info("Enabling Active Directory authentication for test suite")
	err = a.client.Auth.ActiveDirectory.Enable()
	require.NoError(a.T(), err, "Failed to enable Active Directory authentication")
}

func (a *ActiveDirectoryAuthProviderSuite) TearDownSuite() {
	if a.client != nil {
		adConfig, err := a.client.Management.AuthConfig.ByID(authactions.ActiveDirectory)
		if err == nil && adConfig.Enabled {
			logrus.Info("Disabling Active Directory authentication after test suite")
			err := a.client.Auth.ActiveDirectory.Disable()
			if err != nil {
				logrus.WithError(err).Warn("Failed to disable Active Directory in teardown")
			}
		}
	}
	a.session.Cleanup()
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryEnableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := a.client.Auth.ActiveDirectory.Enable()
	require.NoError(a.T(), err, "Failed to enable Active Directory")

	adConfig, err := a.client.Management.AuthConfig.ByID(authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to retrieve Active Directory config")

	require.True(a.T(), adConfig.Enabled, "Active Directory should be enabled")
	require.Equal(a.T(), authactions.AuthProvCleanupAnnotationValUnlocked, adConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := a.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.ActiveDirectoryPasswordSecretID,
		metav1.GetOptions{},
	)
	require.NoError(a.T(), err, "Failed to retrieve password secret")

	require.Equal(a.T(), a.client.Auth.ActiveDirectory.Config.ServiceAccount.Password, string(secret.Data["serviceaccountpassword"]), "Password mismatch")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryDisableAndReenableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := a.client.Auth.ActiveDirectory.Enable()
	require.NoError(a.T(), err, "Failed to enable Active Directory")

	err = a.client.Auth.ActiveDirectory.Disable()
	require.NoError(a.T(), err, "Failed to disable Active Directory")

	adConfig, err := authactions.WaitForAuthProviderAnnotationUpdate(a.client, authactions.ActiveDirectory, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	require.False(a.T(), adConfig.Enabled, "Active Directory should be disabled")
	require.Equal(a.T(), authactions.AuthProvCleanupAnnotationValLocked, adConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be locked")

	_, err = a.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.ActiveDirectoryPasswordSecretID,
		metav1.GetOptions{},
	)
	require.Error(a.T(), err, "Password secret should not exist")
	require.Contains(a.T(), err.Error(), "not found", "Should return not found error")

	err = a.client.Auth.ActiveDirectory.Enable()
	require.NoError(a.T(), err, "Failed to re-enable Active Directory")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(a.T(), err, "All users should be able to login")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryGroupMembershipRefresh() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	adminGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.Group, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	adminGlobalRole := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		GlobalRoleName:     rbac.Admin.String(),
		GroupPrincipalName: adminGroupPrincipalID,
	}

	adminGRB, err := authAdmin.WranglerContext.Mgmt.GlobalRoleBinding().Create(adminGlobalRole)
	require.NoError(a.T(), err, "Failed to create admin global role binding")

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Failed to refresh group membership")

	standardGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.NestedGroup, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	standardGlobalRole := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		GlobalRoleName:     rbac.StandardUser.String(),
		GroupPrincipalName: standardGroupPrincipalID,
	}

	standardGRB, err := authAdmin.WranglerContext.Mgmt.GlobalRoleBinding().Create(standardGlobalRole)
	require.NoError(a.T(), err, "Failed to create standard global role binding")

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Failed to refresh group membership")

	err = authAdmin.WranglerContext.Mgmt.GlobalRoleBinding().Delete(adminGRB.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete admin GRB: %v", adminGRB.Name)

	err = authAdmin.WranglerContext.Mgmt.GlobalRoleBinding().Delete(standardGRB.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete standard GRB: %v", standardGRB.Name)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryNestedGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.ActiveDirectory,
		a.authConfig.DoubleNestedGroup,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range a.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.ActiveDirectory)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(a.T(), a.client, userClient, a.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(a.client, doubleNestedGroupPrincipalID, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to get group CRTB")
	require.NotNil(a.T(), foundCRTB, "Cluster role binding should exist for group")

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.DoubleNestedGroup, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range a.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.ActiveDirectory)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(a.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(a.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryNestedGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project and namespace")

	nestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.NestedGroup, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, nestedGroupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(a.T(), err, "Failed to create PRTB")
	require.NotNil(a.T(), groupPRTBResp, "PRTB should be created")

	for _, userInfo := range a.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.ActiveDirectory)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(a.T(), err, "User [%v] should be able to get project %s", userInfo.Username, projectResp.Name)
	}

	err = authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(groupPRTBResp.Namespace, groupPRTBResp.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete PRTB: %s/%s", groupPRTBResp.Namespace, groupPRTBResp.Name)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.Group, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to create cluster role template binding")

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(a.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	for _, userInfo := range a.authConfig.NestedUsers {
		nestedUserPrincipalID := authactions.GetUserPrincipalID(authactions.ActiveDirectory, userInfo.Username, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

		userPRTB := &managementv3.ProjectRoleTemplateBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    prtbNamespace,
				GenerateName: "prtb-",
			},
			ProjectName:       projectName,
			UserPrincipalName: nestedUserPrincipalID,
			RoleTemplateName:  rbac.ProjectOwner.String(),
		}

		userPRTBResp, err := authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(userPRTB)
		require.NoError(a.T(), err, "Failed to create PRTB for user [%v]", userInfo.Username)
		require.NotNil(a.T(), userPRTBResp, "PRTB should be created for user [%v]", userInfo.Username)
	}
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.DoubleNestedGroup, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to create group cluster role template binding")

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}
	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	nestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.NestedGroup, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, nestedGroupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(a.T(), err, "Failed to create PRTB")
	require.NotNil(a.T(), groupPRTBResp, "PRTB should be created")

	allowedUsers := slices.Concat(a.authConfig.DoubleNestedUsers, a.authConfig.NestedUsers)
	var allowedPrincipalIDs []string
	allowedPrincipalIDs = append(allowedPrincipalIDs, nestedGroupPrincipalID)
	doubleNestedGroupPrincipalID = authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.DoubleNestedGroup, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	allowedPrincipalIDs = append(allowedPrincipalIDs, doubleNestedGroupPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, allowedUsers, "restricted access mode", true)
	require.NoError(a.T(), err, "Cluster/project members should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, a.authConfig.Users, "restricted access mode", false)
	require.NoError(a.T(), err, "Non-members should NOT be able to login")

	_, err = authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupRequiredAccessModePrincipals(
		authAdmin,
		a.cluster.ID,
		a.authConfig,
		authactions.ActiveDirectory,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)
	require.NoError(a.T(), err, "Failed to setup required access mode test")

	newAuthConfig, err := authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeRequired, principalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, a.authConfig.Users, "required access mode", true)
	require.NoError(a.T(), err, "Authorized users should be able to login")

	_, err = authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryRequiredModeNestedGroupAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	nestedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.ActiveDirectory,
		a.authConfig.NestedGroup,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(
		authAdmin,
		a.cluster.ID,
		nestedGroupPrincipalID,
		rbac.ClusterMember.String(),
	)
	require.NoError(a.T(), err, "Failed to create cluster role template binding")

	principalIDs := []string{nestedGroupPrincipalID}

	nestedUsers := slices.Concat(a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	for _, user := range nestedUsers {
		userPrincipalID := authactions.GetUserPrincipalID(
			authactions.ActiveDirectory,
			user.Username,
			a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
			a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
		)
		principalIDs = append(principalIDs, userPrincipalID)
	}

	newAuthConfig, err := authactions.UpdateAccessMode(
		a.client,
		authactions.ActiveDirectory,
		authactions.AccessModeRequired,
		principalIDs,
	)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(
		authAdmin,
		authactions.ActiveDirectory,
		nestedUsers,
		"required access mode with nested groups",
		true,
	)
	require.NoError(a.T(), err, "Nested group members should be able to login")

	_, err = authactions.UpdateAccessMode(
		a.client,
		authactions.ActiveDirectory,
		authactions.AccessModeUnrestricted,
		nil,
	)
	require.NoError(a.T(), err, "Failed to rollback access mode")

	err = a.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryRequiredModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupRequiredAccessModePrincipals(
		authAdmin,
		a.cluster.ID,
		a.authConfig,
		authactions.ActiveDirectory,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)
	require.NoError(a.T(), err, "Failed to setup required access mode test")

	newAuthConfig, err := authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeRequired, principalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	unauthorizedUsers := a.authConfig.TripleNestedUsers
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, unauthorizedUsers, "required access mode", false)
	require.NoError(a.T(), err, "Unauthorized users should NOT be able to login")

	_, err = authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func TestActiveDirectoryAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(ActiveDirectoryAuthProviderSuite))
}

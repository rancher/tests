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
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
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
}

func (a *ActiveDirectoryAuthProviderSuite) TearDownSuite() {
	if a.client != nil {
		adConfig, err := a.client.Management.AuthConfig.ByID(authactions.ActiveDirectory)
		if err == nil && adConfig.Enabled {
			logrus.Info("Disabling Active Directory authentication after test suite")
			err := a.client.Auth.ActiveDirectory.Disable()
			require.NoError(a.T(), err, "Failed to disable Active Directory in teardown")
		}
	}
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryEnableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
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

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryEnableRequiresExplicitAccessMode() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to enable Active Directory")

	configuredAccessMode := a.client.Auth.ActiveDirectory.Config.AccessMode
	subSession.RegisterCleanupFunc(func() error {
		a.client.Auth.ActiveDirectory.Config.AccessMode = configuredAccessMode
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	logrus.Info("Disabling Active Directory so the enable path runs against a fresh provider")
	err = a.client.Auth.ActiveDirectory.Disable()
	require.NoError(a.T(), err, "Failed to disable Active Directory")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(a.client, authactions.ActiveDirectory, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	a.client.Auth.ActiveDirectory.Config.AccessMode = ""

	logrus.Info("Enabling Active Directory with no access mode in the request")
	err = a.client.Auth.ActiveDirectory.Enable()
	require.Error(a.T(), err, "Enabling without an access mode must be rejected so the provider can never come up open to every directory user")
	require.Contains(a.T(), err.Error(), authactions.AccessModeMissingRequiredError, "Enable should be rejected because accessMode is a required field")

	adConfig, err := a.client.Management.AuthConfig.ByID(authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to retrieve Active Directory config")
	require.False(a.T(), adConfig.Enabled, "Active Directory should remain disabled after a rejected enable")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryEnableIntoRestrictedAccessModeIsRejected() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to enable Active Directory")

	configuredAccessMode := a.client.Auth.ActiveDirectory.Config.AccessMode
	subSession.RegisterCleanupFunc(func() error {
		a.client.Auth.ActiveDirectory.Config.AccessMode = configuredAccessMode
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	logrus.Info("Disabling Active Directory so the enable path runs against a fresh provider")
	err = a.client.Auth.ActiveDirectory.Disable()
	require.NoError(a.T(), err, "Failed to disable Active Directory")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(a.client, authactions.ActiveDirectory, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	a.client.Auth.ActiveDirectory.Config.AccessMode = authactions.AccessModeRestricted

	logrus.Info("Enabling Active Directory directly into restricted access mode")
	err = a.client.Auth.ActiveDirectory.Enable()
	require.Error(a.T(), err, "Active Directory cannot be enabled directly into restricted access mode: the enabling admin is never added to allowedPrincipalIDs and every binding for the provider is deleted while it is disabled")
	require.Contains(a.T(), err.Error(), authactions.PermissionDeniedError, "Enable should be rejected with permission denied")

	adConfig, err := a.client.Management.AuthConfig.ByID(authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to retrieve Active Directory config")
	require.False(a.T(), adConfig.Enabled, "Active Directory should remain disabled after a rejected enable")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryDisableAndReenableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
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

	err = authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
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

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, a.cluster.ID)
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

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, a.cluster.ID)
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

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, a.cluster.ID)
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

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryRequiredModeRevokedPrincipalLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	userSearchBase := a.client.Auth.ActiveDirectory.Config.Users.SearchBase
	groupSearchBase := a.client.Auth.ActiveDirectory.Config.Groups.SearchBase

	grantedPrincipalIDs := []string{authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.Group, userSearchBase, groupSearchBase)}
	for _, user := range a.authConfig.Users {
		grantedPrincipalIDs = append(grantedPrincipalIDs, authactions.GetUserPrincipalID(authactions.ActiveDirectory, user.Username, userSearchBase, groupSearchBase))
	}

	logrus.Infof("Granting Active Directory group [%v] and its members access in required mode", a.authConfig.Group)
	grantedConfig, err := authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeRequired, grantedPrincipalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, grantedConfig.AccessMode, "Access mode should be required")
	require.ElementsMatch(a.T(), grantedPrincipalIDs, grantedConfig.AllowedPrincipalIDs, "Granted principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, a.authConfig.Users, "required access mode with principals granted", true)
	require.NoError(a.T(), err, "Granted users should be able to login")

	revokedPrincipalIDs := []string{authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.NestedGroup, userSearchBase, groupSearchBase)}

	logrus.Infof("Revoking Active Directory group [%v] and its members, leaving only group [%v] allowed", a.authConfig.Group, a.authConfig.NestedGroup)
	revokedConfig, err := authactions.UpdateAccessMode(a.client, authactions.ActiveDirectory, authactions.AccessModeRequired, revokedPrincipalIDs)
	require.NoError(a.T(), err, "Failed to revoke principals")
	require.Equal(a.T(), authactions.AccessModeRequired, revokedConfig.AccessMode, "Access mode should remain required")
	require.ElementsMatch(a.T(), revokedPrincipalIDs, revokedConfig.AllowedPrincipalIDs, "Revoked principals should no longer appear in the allow list")

	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, a.authConfig.Users, "required access mode after principals revoked", false)
	require.NoError(a.T(), err, "Revoked users should NOT be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, a.authConfig.NestedUsers, "required access mode after principals revoked", true)
	require.NoError(a.T(), err, "Users in the still-allowed group should be able to login")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchFindsGroups() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupNames := []string{a.authConfig.Group, a.authConfig.NestedGroup, a.authConfig.DoubleNestedGroup}
	for _, groupName := range groupNames {
		logrus.Infof("Searching principals for Active Directory group [%v]", groupName)
		expectedPrincipalID := authactions.GetGroupPrincipalID(
			authactions.ActiveDirectory,
			groupName,
			a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
			a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
		)

		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedPrincipalID)
		require.NoError(a.T(), err, "Group [%v] should be findable through principal search", groupName)
	}
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchFindsUsers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	for _, userInfo := range allUsers {
		logrus.Infof("Searching principals for Active Directory user [%v]", userInfo.Username)
		expectedPrincipalID := authactions.GetUserPrincipalID(
			authactions.ActiveDirectory,
			userInfo.Username,
			a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
			a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
		)

		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, userInfo.Username, expectedPrincipalID)
		require.NoError(a.T(), err, "User [%v] should be findable through principal search", userInfo.Username)
	}
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchFindsLocalUserByDisplayName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Creating a local user whose display name differs from its username")
	localUser, err := userapi.CreateUser(authAdmin)
	require.NoError(a.T(), err, "Failed to create local user")
	require.NotEqual(a.T(), localUser.Username, localUser.DisplayName, "Local user display name must differ from its username for this search to be meaningful")

	logrus.Infof("Searching principals for local user by username [%v]", localUser.Username)
	err = authactions.VerifyPrincipalIsLocal(a.client, localUser.Username)
	require.NoError(a.T(), err, "Local user [%v] should be findable by username", localUser.Username)

	logrus.Infof("Searching principals for local user by display name [%v]", localUser.DisplayName)
	err = authactions.VerifyPrincipalIsLocal(a.client, localUser.DisplayName)
	require.NoError(a.T(), err, "Local user [%v] should be findable by display name [%v]", localUser.Username, localUser.DisplayName)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchProvisionedUserIsNotLocal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userInfo := a.authConfig.Users[0]
	logrus.Infof("Logging in as Active Directory user [%v] to provision the Rancher user", userInfo.Username)
	user := &v3.User{
		Username: userInfo.Username,
		Password: userInfo.Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

	provisionedUser, err := a.client.WranglerContext.Mgmt.User().Get(userClient.UserID, metav1.GetOptions{})
	require.NoError(a.T(), err, "Failed to retrieve the Rancher user provisioned for [%v]", userInfo.Username)
	logrus.Infof("Active Directory user [%v] provisioned Rancher user [%v] with display name [%v], username [%v] and principals %v",
		userInfo.Username, provisionedUser.Name, provisionedUser.DisplayName, provisionedUser.Username, provisionedUser.PrincipalIDs)

	require.Empty(a.T(), provisionedUser.Username, "Externally provisioned user [%v] should carry no local login username", userInfo.Username)
	require.Greater(a.T(), len(provisionedUser.PrincipalIDs), 1, "Externally provisioned user [%v] should carry an external principal alongside the local one", userInfo.Username)

	expectedPrincipalID := authactions.GetUserPrincipalID(
		authactions.ActiveDirectory,
		userInfo.Username,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	for _, searchTerm := range []string{userInfo.Username, provisionedUser.DisplayName} {
		logrus.Infof("Searching principals for provisioned user [%v] using term [%v]", userInfo.Username, searchTerm)
		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, searchTerm, expectedPrincipalID)
		require.NoError(a.T(), err, "Provisioned user [%v] should stay findable when searching [%v]", userInfo.Username, searchTerm)

		err = authactions.VerifyPrincipalNotLocal(authAdmin, searchTerm)
		require.NoError(a.T(), err, "Provisioned user [%v] should not surface as a local principal when searching [%v]", userInfo.Username, searchTerm)
	}
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchByPartialName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := a.authConfig.Group
	require.NotEmpty(a.T(), groupName, "Group name should be set in the auth configuration")
	groupPrefix := groupName[:len(groupName)/2+1]

	logrus.Infof("Searching principals for Active Directory group [%v] using partial name [%v]", groupName, groupPrefix)
	expectedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.ActiveDirectory,
		groupName,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, expectedGroupPrincipalID)
	require.NoError(a.T(), err, "Group [%v] should be findable by partial name [%v]", groupName, groupPrefix)

	userName := a.authConfig.Users[0].Username
	require.NotEmpty(a.T(), userName, "User name should be set in the auth configuration")
	userPrefix := userName[:len(userName)/2+1]

	logrus.Infof("Searching principals for Active Directory user [%v] using partial name [%v]", userName, userPrefix)
	expectedUserPrincipalID := authactions.GetUserPrincipalID(
		authactions.ActiveDirectory,
		userName,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, userPrefix, expectedUserPrincipalID)
	require.NoError(a.T(), err, "User [%v] should be findable by partial name [%v]", userName, userPrefix)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchByPrincipalType() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := a.authConfig.Group
	expectedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.ActiveDirectory,
		groupName,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	logrus.Infof("Searching principals for Active Directory group [%v] restricted to type [%v]", groupName, authactions.PrincipalTypeGroup)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, groupName, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(a.T(), err, "Search restricted to groups should return group [%v] and nothing of another type", groupName)

	userName := a.authConfig.Users[0].Username
	expectedUserPrincipalID := authactions.GetUserPrincipalID(
		authactions.ActiveDirectory,
		userName,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	logrus.Infof("Searching principals for Active Directory user [%v] restricted to type [%v]", userName, authactions.PrincipalTypeUser)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, userName, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(a.T(), err, "Search restricted to users should return user [%v] and nothing of another type", userName)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalByIDResolvesGroupAndUser() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.ActiveDirectory,
		a.authConfig.Group,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	logrus.Infof("Resolving Active Directory group principal [%v] by ID", groupPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.ActiveDirectory, authactions.PrincipalTypeGroup)
	require.NoError(a.T(), err, "Group principal [%v] should resolve by ID", groupPrincipalID)

	userPrincipalID := authactions.GetUserPrincipalID(
		authactions.ActiveDirectory,
		a.authConfig.Users[0].Username,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	logrus.Infof("Resolving Active Directory user principal [%v] by ID", userPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.ActiveDirectory, authactions.PrincipalTypeUser)
	require.NoError(a.T(), err, "User principal [%v] should resolve by ID", userPrincipalID)
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryPrincipalSearchAfterProviderDisabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := a.authConfig.Group
	expectedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.ActiveDirectory,
		groupName,
		a.client.Auth.ActiveDirectory.Config.Users.SearchBase,
		a.client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)

	logrus.Infof("Confirming Active Directory group [%v] is findable while the provider is enabled", groupName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedGroupPrincipalID)
	require.NoError(a.T(), err, "Group [%v] should be findable while Active Directory is enabled", groupName)

	logrus.Info("Disabling Active Directory before searching principals again")
	err = a.client.Auth.ActiveDirectory.Disable()
	require.NoError(a.T(), err, "Failed to disable Active Directory")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(a.client, authactions.ActiveDirectory, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Searching principals for [%v] with Active Directory disabled", groupName)
	err = authactions.VerifyPrincipalSearchExcludesProvider(a.client, groupName, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "No Active Directory principal should be returned while the provider is disabled")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryLoginAttachesExternalPrincipal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	adUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.ActiveDirectory, adUser.Username, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	logrus.Infof("Logging in as Active Directory user %s so that the login flow attaches its external principal", adUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, []authactions.User{adUser}, "external principal attachment", true)
	require.NoError(a.T(), err, "Active Directory user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")
	require.Contains(a.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(a.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (a *ActiveDirectoryAuthProviderSuite) TestActiveDirectoryDisableRemovesExternalPrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	adUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.ActiveDirectory, adUser.Username, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	logrus.Infof("Logging in as Active Directory user %s so that a user record carrying its external principal exists", adUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, []authactions.User{adUser}, "external principal attachment", true)
	require.NoError(a.T(), err, "Active Directory user should be able to login")

	attachedUser, err := userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")
	require.ElementsMatch(a.T(), []string{userPrincipalID, authactions.LocalPrincipalPrefix + attachedUser.Name}, attachedUser.PrincipalIDs,
		"User %s should carry only the Active Directory principal and the local principal added alongside it, so that disabling the provider leaves it with no external identity", attachedUser.Name)

	logrus.Info("Disabling Active Directory so that the cleanup service reconciles principals it emitted")
	err = a.client.Auth.ActiveDirectory.Disable()
	require.NoError(a.T(), err, "Failed to disable Active Directory")

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Active Directory so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(a.client, authactions.ActiveDirectory, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Disabling the provider should leave no user carrying an Active Directory principal")

	logrus.Infof("Verifying that user %s was removed rather than left behind holding only its local principal", attachedUser.Name)
	err = userapi.WaitForUserDeletion(a.client, attachedUser.Name)
	require.NoError(a.T(), err, "Disabling the provider should remove a user whose only external identity that provider emitted, leaving no record that could later be rebound")
}

func TestActiveDirectoryAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(ActiveDirectoryAuthProviderSuite))
}

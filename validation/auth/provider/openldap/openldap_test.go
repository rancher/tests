//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package openldap

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

type OpenLDAPAuthProviderSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	cluster    *v3.Cluster
	adminUser  *v3.User
	authConfig *authactions.AuthConfig
}

func (a *OpenLDAPAuthProviderSuite) SetupSuite() {
	a.session = session.NewSession()

	client, err := rancher.NewClient("", a.session)
	require.NoError(a.T(), err, "Failed to create Rancher client")
	a.client = client

	logrus.Info("Loading auth configuration from config file")
	a.authConfig = new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, a.authConfig)
	require.NotNil(a.T(), a.authConfig, "Auth configuration is not provided")

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(a.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(a.client, clusterName)
	require.NoError(a.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	a.cluster, err = a.client.Management.Cluster.ByID(clusterID)
	require.NoError(a.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Setting up admin user credentials for OpenLDAP authentication")
	a.adminUser = &v3.User{
		Username: client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: client.Auth.OLDAP.Config.Users.Admin.Password,
	}

	logrus.Info("Enabling OpenLDAP authentication for test suite")
	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to enable OpenLDAP authentication")
}

func (a *OpenLDAPAuthProviderSuite) TearDownSuite() {
	if a.client != nil {
		ldapConfig, err := a.client.Management.AuthConfig.ByID(authactions.OpenLdap)
		if err == nil && ldapConfig.Enabled {
			logrus.Info("Disabling OpenLDAP authentication after test suite")
			err := a.client.Auth.OLDAP.Disable()
			if err != nil {
				logrus.WithError(err).Warn("Failed to disable OpenLDAP in teardown")
			}
		}
	}
	a.session.Cleanup()
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPEnableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to enable OpenLDAP")

	ldapConfig, err := a.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to retrieve OpenLDAP config")

	require.True(a.T(), ldapConfig.Enabled, "OpenLDAP should be enabled")
	require.Equal(a.T(), authactions.AuthProvCleanupAnnotationValUnlocked, ldapConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := a.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.OpenLdapPasswordSecretID,
		metav1.GetOptions{},
	)
	require.NoError(a.T(), err, "Failed to retrieve password secret")

	require.Equal(a.T(), a.client.Auth.OLDAP.Config.ServiceAccount.Password, string(secret.Data["serviceaccountpassword"]), "Password mismatch")
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPDisableAndReenableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	err := a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to enable OpenLDAP")

	err = a.client.Auth.OLDAP.Disable()
	require.NoError(a.T(), err, "Failed to disable OpenLDAP")

	ldapConfig, err := authactions.WaitForAuthProviderAnnotationUpdate(a.client, authactions.OpenLdap, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	require.False(a.T(), ldapConfig.Enabled, "OpenLDAP should be disabled")
	require.Equal(a.T(), authactions.AuthProvCleanupAnnotationValLocked, ldapConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be locked")

	_, err = a.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.OpenLdapPasswordSecretID,
		metav1.GetOptions{},
	)
	require.Error(a.T(), err, "Password secret should not exist")
	require.Contains(a.T(), err.Error(), "not found", "Should return not found error")

	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to re-enable OpenLDAP")
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(a.T(), err, "All users should be able to login")
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPGroupMembershipRefresh() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	adminGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.Group, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
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

	standardGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.NestedGroup, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
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

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPNestedGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.OpenLdap,
		a.authConfig.DoubleNestedGroup,
		a.client.Auth.OLDAP.Config.Users.SearchBase,
		a.client.Auth.OLDAP.Config.Groups.SearchBase,
	)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Failed to create cluster role template binding")

	for _, userInfo := range a.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.OpenLdap)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(a.T(), a.client, userClient, a.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(a.client, doubleNestedGroupPrincipalID, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to get group CRTB")
	require.NotNil(a.T(), foundCRTB, "Cluster role binding should exist for group")

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.DoubleNestedGroup, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range a.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.OpenLdap)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(a.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(a.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPNestedGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project and namespace")

	nestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.NestedGroup, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)

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
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.OpenLdap)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(a.T(), err, "User [%v] should be able to get project %s", userInfo.Username, projectResp.Name)
	}

	err = authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(groupPRTBResp.Namespace, groupPRTBResp.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete PRTB: %s/%s", groupPRTBResp.Namespace, groupPRTBResp.Name)
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.Group, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
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
		nestedUserPrincipalID := authactions.GetUserPrincipalID(authactions.OpenLdap, userInfo.Username, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
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

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.DoubleNestedGroup, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to create group cluster role template binding")

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}
	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	nestedGroupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.NestedGroup, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)

	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, nestedGroupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(a.T(), err, "Failed to create PRTB")
	require.NotNil(a.T(), groupPRTBResp, "PRTB should be created")

	var allowedPrincipalIDs []string
	allowedPrincipalIDs = append(allowedPrincipalIDs, nestedGroupPrincipalID)
	doubleNestedGroupPrincipalID = authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.DoubleNestedGroup, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
	allowedPrincipalIDs = append(allowedPrincipalIDs, doubleNestedGroupPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(a.client, authactions.OpenLdap, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	allowedUsers := slices.Concat(a.authConfig.DoubleNestedUsers, a.authConfig.NestedUsers)
	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, allowedUsers, "restricted access mode", true)
	require.NoError(a.T(), err, "Cluster/project members should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, a.authConfig.Users, "restricted access mode", false)
	require.NoError(a.T(), err, "Non-members should NOT be able to login")

	_, err = authactions.UpdateAccessMode(a.client, authactions.OpenLdap, authactions.AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupRequiredAccessModePrincipals(
		authAdmin,
		a.cluster.ID,
		a.authConfig,
		authactions.OpenLdap,
		a.client.Auth.OLDAP.Config.Users.SearchBase,
		a.client.Auth.OLDAP.Config.Groups.SearchBase,
	)
	require.NoError(a.T(), err, "Failed to setup required access mode test")

	newAuthConfig, err := authactions.UpdateAccessMode(a.client, authactions.OpenLdap, authactions.AccessModeRequired, principalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, a.authConfig.Users, "required access mode", true)
	require.NoError(a.T(), err, "Authorized users should be able to login")

	_, err = authactions.UpdateAccessMode(a.client, authactions.OpenLdap, authactions.AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPRequiredModeNestedGroupAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	nestedGroupPrincipalID := authactions.GetGroupPrincipalID(
		authactions.OpenLdap,
		a.authConfig.NestedGroup,
		a.client.Auth.OLDAP.Config.Users.SearchBase,
		a.client.Auth.OLDAP.Config.Groups.SearchBase,
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
		authactions.OpenLdap,
		authactions.AccessModeRequired,
		principalIDs,
	)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(
		authAdmin,
		authactions.OpenLdap,
		nestedUsers,
		"required access mode with nested groups",
		true,
	)
	require.NoError(a.T(), err, "Nested group members should be able to login")

	_, err = authactions.UpdateAccessMode(
		a.client,
		authactions.OpenLdap,
		authactions.AccessModeUnrestricted,
		nil,
	)
	require.NoError(a.T(), err, "Failed to rollback access mode")

	err = a.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(a.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (a *OpenLDAPAuthProviderSuite) TestOpenLDAPRequiredModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupRequiredAccessModePrincipals(
		authAdmin,
		a.cluster.ID,
		a.authConfig,
		authactions.OpenLdap,
		a.client.Auth.OLDAP.Config.Users.SearchBase,
		a.client.Auth.OLDAP.Config.Groups.SearchBase,
	)
	require.NoError(a.T(), err, "Failed to setup required access mode test")

	newAuthConfig, err := authactions.UpdateAccessMode(a.client, authactions.OpenLdap, authactions.AccessModeRequired, principalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	unauthorizedUsers := a.authConfig.TripleNestedUsers
	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, unauthorizedUsers, "required access mode", false)
	require.NoError(a.T(), err, "Unauthorized users should NOT be able to login")

	_, err = authactions.UpdateAccessMode(a.client, authactions.OpenLdap, authactions.AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func TestOpenLDAPAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(OpenLDAPAuthProviderSuite))
}

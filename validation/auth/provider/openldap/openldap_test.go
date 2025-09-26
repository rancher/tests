//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package openldap

import (
	"fmt"
	"slices"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	krbac "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
)

type OpenLDAPAuthProviderSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	cluster    *v3.Cluster
	authConfig *AuthConfig
	adminUser  *v3.User
}

func (a *OpenLDAPAuthProviderSuite) SetupSuite() {
	a.session = session.NewSession()

	client, err := rancher.NewClient("", a.session)
	require.NoError(a.T(), err, "Failed to create Rancher client")
	a.client = client

	logrus.Info("Loading auth configuration from config file")
	a.authConfig = new(AuthConfig)
	config.LoadConfig(ConfigurationFileKey, a.authConfig)
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
		ldapConfig, err := a.client.Management.AuthConfig.ByID(openLdap)
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
func (a *OpenLDAPAuthProviderSuite) TestEnableOpenLDAP() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	client, err := a.client.WithSession(subSession)
	require.NoError(a.T(), err, "Failed to create client with new session")

	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to enable OpenLDAP")

	ldapConfig, err := a.client.Management.AuthConfig.ByID(openLdap)
	require.NoError(a.T(), err, "Failed to retrieve OpenLDAP config")

	require.True(a.T(), ldapConfig.Enabled, "OpenLDAP should be enabled")
	require.Equal(a.T(), authProvCleanupAnnotationValUnlocked, ldapConfig.Annotations[authProvCleanupAnnotationKey], "Annotation should be unlocked")

	passwordSecretResp, err := client.Steve.SteveType("secret").ByID(passwordSecretID)
	require.NoError(a.T(), err, "Failed to retrieve password secret")

	passwordSecret := &corev1.Secret{}
	require.NoError(a.T(), v1.ConvertToK8sType(passwordSecretResp.JSONResp, passwordSecret), "Failed to convert secret")

	require.Equal(a.T(), client.Auth.OLDAP.Config.ServiceAccount.Password, string(passwordSecret.Data["serviceaccountpassword"]), "Password mismatch")
}

func (a *OpenLDAPAuthProviderSuite) TestDisableOpenLDAP() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	client, err := a.client.WithSession(subSession)
	require.NoError(a.T(), err, "Failed to create client with new session")

	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to enable OpenLDAP")

	err = client.Auth.OLDAP.Disable()
	require.NoError(a.T(), err, "Failed to disable OpenLDAP")

	ldapConfig, err := waitForAuthProviderAnnotationUpdate(client, authProvCleanupAnnotationValLocked)
	require.NoError(a.T(), err, "Failed waiting for annotation update")

	require.False(a.T(), ldapConfig.Enabled, "OpenLDAP should be disabled")
	require.Equal(a.T(), authProvCleanupAnnotationValLocked, ldapConfig.Annotations[authProvCleanupAnnotationKey], "Annotation should be locked")

	_, err = client.Steve.SteveType("secret").ByID(passwordSecretID)
	require.Error(a.T(), err, "Password secret should not exist")
	require.Contains(a.T(), err.Error(), "404", "Should return 404 error")

	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to re-enable OpenLDAP")
}

func (a *OpenLDAPAuthProviderSuite) TestAllowAnyUserAccessMode() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	err = verifyUserLogins(authAdmin, auth.OpenLDAPAuth, allUsers, "unrestricted access mode", true)
	require.NoError(a.T(), err, "All users should be able to login")
}

func (a *OpenLDAPAuthProviderSuite) TestRefreshGroup() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	adminGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.Groups)
	adminGlobalRole := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		GlobalRoleName:     rbac.Admin.String(),
		GroupPrincipalName: adminGroupPrincipalID,
	}

	_, err = krbac.CreateGlobalRoleBinding(authAdmin, adminGlobalRole)
	require.NoError(a.T(), err, "Failed to create admin global role binding")

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Failed to refresh group membership")

	standardGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.NestedGroup)
	standardGlobalRole := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		GlobalRoleName:     rbac.StandardUser.String(),
		GroupPrincipalName: standardGroupPrincipalID,
	}

	_, err = krbac.CreateGlobalRoleBinding(authAdmin, standardGlobalRole)
	require.NoError(a.T(), err, "Failed to create standard global role binding")

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Failed to refresh group membership")
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembershipDoubleNestedGroupClusterAccess() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.DoubleNestedGroup)
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Failed to create cluster role binding")

	for _, userInfo := range a.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := loginAsAuthUser(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		newUserClient, err := userClient.ReLogin()
		require.NoError(a.T(), err, "Failed to relogin user [%v]", userInfo.Username)

		clusterList, err := newUserClient.Steve.SteveType("management.cattle.io.cluster").List(nil)
		require.NoError(a.T(), err, "Failed to list clusters for user [%v]", userInfo.Username)
		require.Equal(a.T(), 1, len(clusterList.Data), "User [%v] should see exactly 1 cluster", userInfo.Username)
	}

	crtbList, err := krbac.ListClusterRoleTemplateBindings(a.client, metav1.ListOptions{})
	require.NoError(a.T(), err, "Failed to list cluster role bindings")

	var foundCRTB *managementv3.ClusterRoleTemplateBinding
	for _, crtb := range crtbList.Items {
		if crtb.GroupPrincipalName == doubleNestedGroupPrincipalID && crtb.ClusterName == a.cluster.ID {
			foundCRTB = &crtb
			break
		}
	}

	require.NotNil(a.T(), foundCRTB, "Cluster role binding should exist for group")
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembershipOtherUsersCannotAccessCluster() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.DoubleNestedGroup)
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Failed to create cluster role binding")

	for _, userInfo := range a.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := loginAsAuthUser(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(a.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(a.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembershipNestedGroupProjectAccess() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project and namespace")

	nestedGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.NestedGroup)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	groupPRTBResp, err := rbac.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, nestedGroupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(a.T(), err, "Failed to create PRTB")
	require.NotNil(a.T(), groupPRTBResp, "PRTB should be created")

	for _, userInfo := range a.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := loginAsAuthUser(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err, "Failed to login user [%v]", userInfo.Username)

		newUserClient, err := userClient.ReLogin()
		require.NoError(a.T(), err, "Failed to relogin user [%v]", userInfo.Username)

		projectList, err := newUserClient.Steve.SteveType("management.cattle.io.project").List(nil)
		require.NoError(a.T(), err, "User [%v] should be able to list projects", userInfo.Username)
		require.Greater(a.T(), len(projectList.Data), 0, "User [%v] should see at least 1 project", userInfo.Username)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestRestrictedAccessModeClusterAndProjectBindings() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.Groups)
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to create cluster role binding")

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = waitForNamespaceReady(authAdmin, prtbNamespace, defaults.OneMinuteTimeout)
	require.NoError(a.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	for _, userInfo := range a.authConfig.NestedUsers {
		nestedUserPrincipalID := getUserPrincipalID(a.client, userInfo.Username)

		userPRTB := &managementv3.ProjectRoleTemplateBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    prtbNamespace,
				GenerateName: "prtb-",
			},
			ProjectName:       projectName,
			UserPrincipalName: nestedUserPrincipalID,
			RoleTemplateName:  rbac.ProjectOwner.String(),
		}

		userPRTBResp, err := krbac.CreateProjectRoleTemplateBinding(authAdmin, userPRTB)
		require.NoError(a.T(), err, "Failed to create PRTB for user [%v]", userInfo.Username)
		require.NotNil(a.T(), userPRTBResp, "PRTB should be created for user [%v]", userInfo.Username)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	doubleNestedGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.DoubleNestedGroup)
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to create cluster role binding")

	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}
	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	nestedGroupPrincipalID := getGroupPrincipalID(a.client, a.authConfig.NestedGroup)

	groupPRTBResp, err := rbac.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, nestedGroupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(a.T(), err, "Failed to create PRTB")
	require.NotNil(a.T(), groupPRTBResp, "PRTB should be created")

	newAuthConfig, err := updateAccessMode(a.client, AccessModeRestricted, nil)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	allowedUsers := slices.Concat(a.authConfig.DoubleNestedUsers, a.authConfig.NestedUsers)
	err = verifyUserLogins(authAdmin, auth.OpenLDAPAuth, allowedUsers, "restricted access mode", true)
	require.NoError(a.T(), err, "Cluster/project members should be able to login")

	err = verifyUserLogins(authAdmin, auth.OpenLDAPAuth, a.authConfig.Users, "restricted access mode", false)
	require.NoError(a.T(), err, "Non-members should NOT be able to login")

	_, err = updateAccessMode(a.client, AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}
func (a *OpenLDAPAuthProviderSuite) TestRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := setupRequiredAccessModeTest(a.client, authAdmin, a.cluster.ID, a.authConfig)
	require.NoError(a.T(), err, "Failed to setup required access mode test")

	newAuthConfig, err := updateAccessMode(a.client, AccessModeRequired, principalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = verifyUserLogins(authAdmin, auth.OpenLDAPAuth, a.authConfig.Users, "required access mode", true)
	require.NoError(a.T(), err, "Authorized users should be able to login")

	_, err = updateAccessMode(a.client, AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func (a *OpenLDAPAuthProviderSuite) TestRestrictedAccessModeUnauthorizedUsersCannotLogin() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := setupRequiredAccessModeTest(a.client, authAdmin, a.cluster.ID, a.authConfig)
	require.NoError(a.T(), err, "Failed to setup required access mode test")

	newAuthConfig, err := updateAccessMode(a.client, AccessModeRequired, principalIDs)
	require.NoError(a.T(), err, "Failed to update access mode")
	require.Equal(a.T(), AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	unauthorizedUsers := slices.Concat(a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	err = verifyUserLogins(authAdmin, auth.OpenLDAPAuth, unauthorizedUsers, "required access mode", false)
	require.NoError(a.T(), err, "Unauthorized users should NOT be able to login")

	_, err = updateAccessMode(a.client, AccessModeUnrestricted, nil)
	require.NoError(a.T(), err, "Failed to rollback access mode")
}

func TestOpenLDAPAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(OpenLDAPAuthProviderSuite))
}

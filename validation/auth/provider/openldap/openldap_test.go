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
	require.NoError(a.T(), err)
	a.client = client

	logrus.Info("Loading auth configuration from config file")
	a.authConfig = new(AuthConfig)
	config.LoadConfig(ConfigurationFileKey, a.authConfig)

	require.NotNil(a.T(), a.authConfig, "Auth configuration is not provided, skipping the tests")

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(a.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(a.client, clusterName)
	require.NoError(a.T(), err, "Error getting cluster ID")

	a.cluster, err = a.client.Management.Cluster.ByID(clusterID)
	require.NoError(a.T(), err)

	logrus.Info("Setting up admin user credentials for OpenLDAP authentication")
	a.adminUser = &v3.User{
		Username: client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: client.Auth.OLDAP.Config.Users.Admin.Password,
	}

	logrus.Info("Enabling OpenLDAP authentication for test suite")
	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err)
	logrus.Info("OpenLDAP authentication enabled successfully")
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

	logrus.Info("Attempting to enable OpenLDAP")
	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err, "Failed to enable OpenLDAP")

	logrus.Info("Retrieving OpenLDAP configuration")
	ldapConfig, err := a.client.Management.AuthConfig.ByID(openLdap)
	require.NoError(a.T(), err, "Failed to retrieve OpenLDAP config")
	logrus.WithFields(logrus.Fields{
		"enabled":     ldapConfig.Enabled,
		"annotations": ldapConfig.Annotations,
	}).Info("Retrieved OpenLDAP config successfully")

	require.Truef(a.T(), ldapConfig.Enabled, "Checking if OpenLDAP is enabled")
	require.Equalf(a.T(), authProvCleanupAnnotationValUnlocked, ldapConfig.Annotations[authProvCleanupAnnotationKey], "Annotation should be set to unlocked for LDAP Auth Config")

	passwordSecretResp, err := client.Steve.SteveType("secret").ByID(passwordSecretID)
	require.NoError(a.T(), err, "Failed to retrieve password secret")

	passwordSecret := &corev1.Secret{}
	require.NoError(a.T(), v1.ConvertToK8sType(passwordSecretResp.JSONResp, passwordSecret), "Failed to convert secret to k8s type")

	require.Equal(a.T(), client.Auth.OLDAP.Config.ServiceAccount.Password,
		string(passwordSecret.Data["serviceaccountpassword"]), "Service account password does not match expected value")
}

func (a *OpenLDAPAuthProviderSuite) TestDisableOpenLDAP() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	client, err := a.client.WithSession(subSession)
	require.NoError(a.T(), err)

	logrus.Info("Enabling OpenLDAP first before testing disable")
	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err)

	logrus.Info("Verifying OpenLDAP is enabled")
	_, err = a.client.Management.AuthConfig.ByID(openLdap)
	require.NoError(a.T(), err)

	logrus.Info("Attempting to disable OpenLDAP")
	err = client.Auth.OLDAP.Disable()
	require.NoError(a.T(), err)

	logrus.Info("Waiting for annotation update")
	ldapConfig, err := waitForAuthProviderAnnotationUpdate(client)
	require.NoError(a.T(), err)

	require.Falsef(a.T(), ldapConfig.Enabled, "Checking if OpenLDAP is disabled")
	require.Equalf(a.T(), authProvCleanupAnnotationValLocked, ldapConfig.Annotations[authProvCleanupAnnotationKey], "Checking if annotation set to locked for LDAP Auth Config")

	logrus.WithField("secretID", passwordSecretID).Info("Verifying password secret removal")
	_, err = client.Steve.SteveType("secret").ByID(passwordSecretID)
	require.Error(a.T(), err, "Password secret should not exist after OLDAP disable")
	require.Contains(a.T(), err.Error(), "404", "Password secret removal should return 404 error")

	logrus.Info("Re-enabling OpenLDAP after disable test to not interfere with other tests")
	err = a.client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err)
}

func (a *OpenLDAPAuthProviderSuite) TestAllowAnyUserAccessMode() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	allUsers := slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers)
	verifyUserLogins(authAdmin, auth.OpenLDAPAuth, allUsers, "should be able to login in any user access mode", true)
}

func (a *OpenLDAPAuthProviderSuite) TestRefreshGroup() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase
	groupName := a.authConfig.Group
	nestedGroupName := a.authConfig.NestedGroup

	adminGroupPrincipalID := newPrincipalID(openLdap, "group", groupName, searchBase)
	newAdminGlobalRole := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		GlobalRoleName:     rbac.Admin.String(),
		GroupPrincipalName: adminGroupPrincipalID,
	}

	logrus.Infof("Creating global role binding for group [%v] with [%v] role", newAdminGlobalRole.GroupPrincipalName, newAdminGlobalRole.GlobalRoleName)

	_, err = krbac.CreateGlobalRoleBinding(authAdmin, newAdminGlobalRole)
	require.NoError(a.T(), err, "Error occurred while creating a role [%v]", newAdminGlobalRole)

	logrus.Infof("Refreshing group membership for group [%v]", groupName)

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Error occurred refreshing the group membership for group %v", groupName)

	standardGroupPrincipalID := newPrincipalID(openLdap, "group", nestedGroupName, searchBase)
	newStandardGlobalRole := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		GlobalRoleName:     rbac.StandardUser.String(),
		GroupPrincipalName: standardGroupPrincipalID,
	}

	logrus.Infof("Creating global role binding for group [%v] with [%v] role", newStandardGlobalRole.GroupPrincipalName, newStandardGlobalRole.GlobalRoleName)

	_, err = krbac.CreateGlobalRoleBinding(authAdmin, newStandardGlobalRole)
	require.NoError(a.T(), err, "Error occurred while creating a role %v", newStandardGlobalRole)

	logrus.Infof("Refreshing group membership for group [%v]", nestedGroupName)
	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Error occurred refreshing the group membership for group [%v]", nestedGroupName)
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembershipDoubleNestedGroupClusterAccess() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase
	doubleNestedGroupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.DoubleNestedGroup, searchBase)

	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err, "Error occurred while creating cluster role template binding")

	for _, userInfo := range a.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := loginAsAuthUser(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err)

		newUserClient, err := userClient.ReLogin()
		require.NoError(a.T(), err)

		clusterList, err := newUserClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NoError(a.T(), err)

		logrus.Infof("User [%v] lists [%v] clusters while expecting [%v] clusters", userInfo.Username, len(clusterList.Data), 1)
		require.Equalf(a.T(), 1, len(clusterList.Data), "User [%v] should list exactly 1 cluster", userInfo.Username)
	}

	logrus.Info("Verifying cluster role template binding was created")
	crtbList, err := krbac.ListClusterRoleTemplateBindings(a.client, metav1.ListOptions{})
	require.NoError(a.T(), err)

	var foundCRTB *managementv3.ClusterRoleTemplateBinding
	for _, crtb := range crtbList.Items {
		if crtb.GroupPrincipalName == doubleNestedGroupPrincipalID && crtb.ClusterName == a.cluster.ID {
			foundCRTB = &crtb
			break
		}
	}

	require.NotNil(a.T(), foundCRTB, "Cluster role template binding should be found for group [%v]", doubleNestedGroupPrincipalID)
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembershipOtherUsersCannotAccessCluster() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase
	doubleNestedGroupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.DoubleNestedGroup, searchBase)

	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(a.T(), err)

	for _, userInfo := range slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers) {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := loginAsAuthUser(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err)

		logrus.Infof("Verifying user [%v] should NOT list clusters", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)

		require.NotNil(a.T(), err, "User [%v] should NOT be able to list clusters", userInfo.Username)
		require.Contains(a.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Error should indicate insufficient permissions for user [%v]", userInfo.Username)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembershipNestedGroupProjectAccess() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	logrus.Info("Creating project and namespace with admin using wrangler")
	projectResp, createdNamespace, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err)

	nestedGroupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.NestedGroup, searchBase)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	logrus.Infof("Creating PRTB for group [%v] with principal ID [%v]", a.authConfig.NestedGroup, nestedGroupPrincipalID)

	groupPRTBResp, err := rbac.CreateGroupProjectRoleTemplateBinding(
		authAdmin,
		projectName,
		prtbNamespace,
		nestedGroupPrincipalID,
		rbac.ProjectOwner.String(),
	)
	require.NoError(a.T(), err, "Error occurred while creating project role template binding")
	require.NotNil(a.T(), groupPRTBResp, "Error occurred while: creating a PRTB for group [%v]", a.authConfig.NestedGroup)

	for _, userInfo := range a.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		_, err := loginAsAuthUser(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err)

		logrus.Infof("User [%v] has access to namespace [%v]", userInfo.Username, createdNamespace.Name)
		require.Equal(a.T(), createdNamespace.Name, createdNamespace.Name, "User [%v] should have access to namespace [%v]", userInfo.Username, createdNamespace.Name)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestRestrictedAccessModeSetupClusterAndProjectBindings() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	groupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.Group, searchBase)
	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", groupPrincipalID, rbac.ClusterMember.String())

	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Error occurred while creating cluster role template binding")

	defaultProject, err := projects.GetProjectByName(authAdmin, a.cluster.ID, "Default")
	require.NoError(a.T(), err)

	for _, userInfo := range a.authConfig.NestedUsers {
		nestedUserPrincipalID := newPrincipalID(openLdap, "user", userInfo.Username, searchBase)

		projectName := defaultProject.ID
		prtbNamespace := defaultProject.Name

		userPRTB := &managementv3.ProjectRoleTemplateBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    prtbNamespace,
				GenerateName: "prtb-",
			},
			ProjectName:       projectName,
			UserPrincipalName: nestedUserPrincipalID,
			RoleTemplateName:  rbac.ProjectOwner.String(),
		}

		logrus.Infof("Creating project role template binding for user [%v] with role [%v]", userInfo.Username, rbac.ProjectOwner.String())

		userPRTBResp, err := krbac.CreateProjectRoleTemplateBinding(authAdmin, userPRTB)
		require.NoError(a.T(), err)

		logrus.Infof("Project role template binding created for user [%v]", userInfo.Username)
		require.NotNil(a.T(), userPRTBResp, "Project role template binding should be created for user [%v]", userInfo.Username)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	groupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.Group, searchBase)
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err)

	var principalIDs []string
	principalIDs = append(principalIDs, newPrincipalID(openLdap, "group", a.authConfig.DoubleNestedGroup, searchBase))
	for _, v := range a.authConfig.DoubleNestedUsers {
		principalIDs = append(principalIDs, newPrincipalID(openLdap, "user", v.Username, searchBase))
	}

	logrus.Info("Updating access mode to restrict access to only authorized users & groups")

	existing, updates, err := newAuthConfigWithAccessMode(a.client, openLdap, "required", principalIDs)
	require.NoError(a.T(), err)
	newAuthConfig, err := a.client.Auth.OLDAP.Update(existing, updates)
	require.NoError(a.T(), err)

	require.Equal(a.T(), "required", newAuthConfig.AccessMode, "Access mode should be updated to required")

	verifyUserLogins(authAdmin, auth.OpenLDAPAuth, a.authConfig.DoubleNestedUsers, "can login in restricted mode", true)

	logrus.Info("Rolling back access mode to unrestricted")
	authExisting, authWithUnrestricted, err := newAuthConfigWithAccessMode(a.client, openLdap, "unrestricted", nil)
	require.NoError(a.T(), err)

	newAuthConfig, err = a.client.Auth.OLDAP.Update(authExisting, authWithUnrestricted)
	require.NoError(a.T(), err)
	require.Equal(a.T(), "unrestricted", newAuthConfig.AccessMode, "Access mode should be rolled back to unrestricted")
}

func (a *OpenLDAPAuthProviderSuite) TestRestrictedAccessModeUnauthorizedUsersCannotLogin() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	groupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.Group, searchBase)
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err)

	var principalIDs []string
	principalIDs = append(principalIDs, newPrincipalID(openLdap, "group", a.authConfig.DoubleNestedGroup, searchBase))
	for _, v := range a.authConfig.DoubleNestedUsers {
		principalIDs = append(principalIDs, newPrincipalID(openLdap, "user", v.Username, searchBase))
	}

	logrus.Info("Updating access mode to restrict access to only authorized users & groups")

	existing, updates, err := newAuthConfigWithAccessMode(a.client, openLdap, "required", principalIDs)
	require.NoError(a.T(), err)
	newAuthConfig, err := a.client.Auth.OLDAP.Update(existing, updates)
	require.NoError(a.T(), err)

	require.Equal(a.T(), "required", newAuthConfig.AccessMode, "Access mode should be updated to required")

	otherUsers := slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers)
	verifyUserLogins(authAdmin, auth.OpenLDAPAuth, otherUsers, "cannot login in restricted mode", false)

	logrus.Info("Rolling back access mode to unrestricted")
	authExisting, authWithUnrestricted, err := newAuthConfigWithAccessMode(a.client, openLdap, "unrestricted", nil)
	require.NoError(a.T(), err)

	newAuthConfig, err = a.client.Auth.OLDAP.Update(authExisting, authWithUnrestricted)
	require.NoError(a.T(), err)
	require.Equal(a.T(), "unrestricted", newAuthConfig.AccessMode, "Access mode should be rolled back to unrestricted")
}

func (a *OpenLDAPAuthProviderSuite) TestAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := setupAuthenticatedTest(a.client, a.session, a.adminUser)
	require.NoError(a.T(), err)
	defer subSession.Cleanup()

	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	doubleNestedGroupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.DoubleNestedGroup, searchBase)

	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", doubleNestedGroupPrincipalID, rbac.ClusterMember.String())
	_, err = rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, a.cluster.ID, doubleNestedGroupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Error occurred while creating cluster role template binding")

	logrus.Info("Creating project and namespace with admin using wrangler")
	projectResp, _, err := projects.CreateProjectAndNamespaceUsingWrangler(authAdmin, a.cluster.ID)
	require.NoError(a.T(), err)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}
	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	nestedGroupPrincipalID := newPrincipalID(openLdap, "group", a.authConfig.NestedGroup, searchBase)

	logrus.Infof("Creating project role template binding for group [%v] with role [%v]", nestedGroupPrincipalID, rbac.ProjectOwner.String())
	groupPRTB := &managementv3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    prtbNamespace,
			GenerateName: "prtb-",
		},
		ProjectName:        projectName,
		GroupPrincipalName: nestedGroupPrincipalID,
		RoleTemplateName:   rbac.ProjectOwner.String(),
	}

	groupPRTBResp, err := krbac.CreateProjectRoleTemplateBinding(authAdmin, groupPRTB)
	require.NoError(a.T(), err)
	require.NotNil(a.T(), groupPRTBResp)

	logrus.Infof("Project role binding created for group [%v]", a.authConfig.NestedGroup)
	require.NotNil(a.T(), groupPRTBResp, "Project role binding should be created for group [%v]", a.authConfig.NestedGroup)

	logrus.Info("Updating access mode to allow cluster and project members")

	authExisting, authWithRestricted, err := newAuthConfigWithAccessMode(a.client, openLdap, "restricted", nil)
	require.NoError(a.T(), err)
	newAuthConfig, err := a.client.Auth.OLDAP.Update(authExisting, authWithRestricted)
	require.NoError(a.T(), err)

	require.Equal(a.T(), "restricted", newAuthConfig.AccessMode, "Access mode should be updated to restricted")

	allowedUsers := slices.Concat(a.authConfig.DoubleNestedUsers, a.authConfig.NestedUsers)
	verifyUserLogins(authAdmin, auth.OpenLDAPAuth, allowedUsers, "can login in cluster/project members mode", true)
	verifyUserLogins(authAdmin, auth.OpenLDAPAuth, a.authConfig.Users, "cannot login in cluster/project members mode", false)

	logrus.Info("Rolling back access mode to unrestricted from cluster/project members mode")
	authExisting, authWithUnrestricted, err := newAuthConfigWithAccessMode(a.client, openLdap, "unrestricted", nil)
	require.NoError(a.T(), err)

	newAuthConfig, err = a.client.Auth.OLDAP.Update(authExisting, authWithUnrestricted)
	require.NoError(a.T(), err)
	require.Equal(a.T(), "unrestricted", newAuthConfig.AccessMode, "Access mode should be rolled back to unrestricted")
}

func TestOpenLDAPAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(OpenLDAPAuthProviderSuite))
}

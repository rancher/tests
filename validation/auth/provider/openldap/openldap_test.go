package openldap

import (
	"slices"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
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
	assert.NoError(a.T(), err)
	a.client = client

	logrus.Info("Loading auth configuration from config file")
	a.authConfig = new(AuthConfig)
	config.LoadConfig(ConfigurationFileKey, a.authConfig)

	if a.authConfig == nil {
		a.T().Skipf("Auth configuration is not provided, skipping the tests")
	}

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(a.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(a.client, clusterName)
	require.NoError(a.T(), err, "Error getting cluster ID")

	a.cluster, err = a.client.Management.Cluster.ByID(clusterID)
	assert.NoError(a.T(), err)

	logrus.Info("Setting up admin user credentials for OLDAP authentication")
	a.adminUser = &v3.User{
		Username: client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: client.Auth.OLDAP.Config.Users.Admin.Password,
	}

	logrus.Info("Enabling OLDAP authentication for test suite")
	err = a.client.Auth.OLDAP.Enable()
	if err != nil {
		logrus.WithError(err).Error("Failed to enable OLDAP in setup")
	}
	require.NoError(a.T(), err)
	logrus.Info("OLDAP authentication enabled successfully")
}

func (a *OpenLDAPAuthProviderSuite) TearDownSuite() {
	if a.client != nil {
		logrus.Info("Disabling OLDAP authentication after test suite")
		err := a.client.Auth.OLDAP.Disable()
		if err != nil {
			logrus.WithError(err).Warn("Failed to disable OLDAP in teardown")
		}
	}
	a.session.Cleanup()
}

// ensureOLDAPEnabled ensures OLDAP is enabled before setting up authenticated test
func (a *OpenLDAPAuthProviderSuite) ensureOLDAPEnabled() {
	ldapConfig, err := a.client.Management.AuthConfig.ByID("openldap")
	if err != nil {
		logrus.WithError(err).Error("Failed to check OLDAP status")
		return
	}

	if !ldapConfig.Enabled {
		logrus.Info("OLDAP is disabled, re-enabling for test")
		err = a.client.Auth.OLDAP.Enable()
		if err != nil {
			logrus.WithError(err).Error("Failed to re-enable OLDAP")
		}
		require.NoError(a.T(), err, "Failed to re-enable OLDAP for test")
	}
}

// setupAuthenticatedTest creates a new session and authenticated admin client for tests that need auth
func (a *OpenLDAPAuthProviderSuite) setupAuthenticatedTest() (*session.Session, *rancher.Client) {
	a.ensureOLDAPEnabled()

	subSession := a.session.NewSession()

	client, err := a.client.WithSession(subSession)
	require.NoError(a.T(), err, "Failed to create client with new session")

	authAdmin, err := login(client, auth.OpenLDAPAuth, a.adminUser)
	require.NoError(a.T(), err, "Failed to authenticate admin")

	return subSession, authAdmin
}

func (a *OpenLDAPAuthProviderSuite) TestEnableOLDAP() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	client, err := a.client.WithSession(subSession)
	require.NoError(a.T(), err)

	logrus.Info("Attempting to enable OLDAP")
	err = client.Auth.OLDAP.Enable()
	if err != nil {
		logrus.WithError(err).Error("Failed to enable OLDAP")
	}
	require.NoError(a.T(), err)

	logrus.Info("Retrieving OLDAP configuration")
	ldapConfig, err := client.Management.AuthConfig.ByID("openldap")
	if err != nil {
		logrus.WithError(err).Error("Failed to retrieve OLDAP config")
	} else {
		logrus.WithFields(logrus.Fields{
			"enabled":     ldapConfig.Enabled,
			"annotations": ldapConfig.Annotations,
		}).Info("Retrieved OLDAP config successfully")
	}
	require.NoError(a.T(), err)

	assert.Truef(a.T(), ldapConfig.Enabled, "Checking if Open LDAP has enabled")
	assert.Equalf(a.T(), authProvCleanupAnnotationValUnlocked, ldapConfig.Annotations[authProvCleanupAnnotationKey], "Checking if annotation set to unlocked for LDAP Auth Config")

	logrus.WithField("secretID", passwordSecretID).Info("Checking password secret")
	passwordSecretResp, err := client.Steve.SteveType("secret").ByID(passwordSecretID)
	require.NoError(a.T(), err)
	assert.NoErrorf(a.T(), err, "Checking open LDAP config secret for service account password exists")

	passwordSecret := &corev1.Secret{}
	err = v1.ConvertToK8sType(passwordSecretResp.JSONResp, passwordSecret)
	require.NoError(a.T(), err)

	assert.Equal(a.T(), client.Auth.OLDAP.Config.ServiceAccount.Password, string(passwordSecret.Data["serviceaccountpassword"]), "Checking if serviceaccountpassword value is equal to the given")
}

func (a *OpenLDAPAuthProviderSuite) TestDisableOLDAP() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	client, err := a.client.WithSession(subSession)
	require.NoError(a.T(), err)

	logrus.Info("Enabling OLDAP first before testing disable")
	err = client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err)

	logrus.Info("Verifying OLDAP is enabled")
	_, err = client.Management.AuthConfig.ByID("openldap")
	require.NoError(a.T(), err)

	logrus.Info("Attempting to disable OLDAP")
	err = client.Auth.OLDAP.Disable()
	require.NoError(a.T(), err)

	logrus.Info("Waiting for annotation update")
	ldapConfig, err := waitUntilAnnotationIsUpdated(client)
	require.NoError(a.T(), err)

	assert.Falsef(a.T(), ldapConfig.Enabled, "Checking if Open LDAP is disabled")

	assert.Equalf(a.T(), authProvCleanupAnnotationValLocked, ldapConfig.Annotations[authProvCleanupAnnotationKey], "Checking if annotation set to locked for LDAP Auth Config")

	logrus.WithField("secretID", passwordSecretID).Info("Verifying password secret removal")
	_, err = client.Steve.SteveType("secret").ByID(passwordSecretID)
	if err != nil {
		logrus.WithError(err).Info("Password secret not found as expected")
	} else {
		logrus.Warn("Password secret still exists unexpectedly")
	}
	assert.Errorf(a.T(), err, "Checking open LDAP config secret for service account password does not exist")
	assert.Containsf(a.T(), err.Error(), "404", "Checking open LDAP config secret for service account password error returns 404")

	logrus.Info("Re-enabling OLDAP after disable test to not interfere with other tests")
	err = client.Auth.OLDAP.Enable()
	require.NoError(a.T(), err)
}

func (a *OpenLDAPAuthProviderSuite) TestAllowAnyUserAccessMode() {
	subSession, authAdmin := a.setupAuthenticatedTest()
	defer subSession.Cleanup()

	for _, v := range slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers, a.authConfig.DoubleNestedUsers) {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}

		logrus.Infof("Verifying login for user [%v]", v.Username)

		_, err := login(authAdmin, auth.OpenLDAPAuth, user)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"username": v.Username,
				"error":    err.Error(),
			}).Error("User login failed")
		} else {
			logrus.WithField("username", v.Username).Info("User login successful")
		}
		require.NoError(a.T(), err)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestRefreshGroup() {
	subSession, authAdmin := a.setupAuthenticatedTest()
	defer subSession.Cleanup()

	authConfigID := "openldap"
	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase
	groupName := a.authConfig.Group
	nestedGroupName := a.authConfig.NestedGroup

	adminGroupPrincipalID := newPrincipalID(authConfigID, "group", groupName, searchBase)
	newAdminGlobalRole := &v3.GlobalRoleBinding{
		GlobalRoleID:     rbac.Admin.String(),
		GroupPrincipalID: adminGroupPrincipalID,
	}

	logrus.Infof("Creating global role binding for group [%v] with [%v] role", newAdminGlobalRole.GroupPrincipalID, newAdminGlobalRole.GlobalRoleID)

	_, err := authAdmin.Management.GlobalRoleBinding.Create(newAdminGlobalRole)
	if err != nil {
		logrus.WithError(err).Error("Failed to create admin global role binding")
	}
	require.NoError(a.T(), err, "Error occurred while creating a role [%v]", newAdminGlobalRole)

	logrus.Infof("Refreshing group membership for group [%v]", groupName)

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Error occurred refreshing the group membership for group %v", groupName)

	standardGroupPrincipalID := newPrincipalID(authConfigID, "group", nestedGroupName, searchBase)
	newStandardGlobalRole := &v3.GlobalRoleBinding{
		GlobalRoleID:     rbac.StandardUser.String(),
		GroupPrincipalID: standardGroupPrincipalID,
	}

	logrus.Infof("Creating global role binding for group [%v] with [%v] role", newStandardGlobalRole.GroupPrincipalID, newStandardGlobalRole.GlobalRoleID)

	_, err = authAdmin.Management.GlobalRoleBinding.Create(newStandardGlobalRole)
	require.NoError(a.T(), err, "Error occurred while creating a role %v", newStandardGlobalRole)

	logrus.Infof("Refreshing group membership for group [%v]", nestedGroupName)
	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Error occurred refreshing the group membership for group [%v]", nestedGroupName)
}

func (a *OpenLDAPAuthProviderSuite) TestGroupMembership() {
	subSession, authAdmin := a.setupAuthenticatedTest()
	defer subSession.Cleanup()

	authConfigID := "openldap"
	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	doubleNestedGroupPrincipalID := newPrincipalID(authConfigID, "group", a.authConfig.DoubleNestedGroup, searchBase)
	groupCRTB := &v3.ClusterRoleTemplateBinding{
		ClusterID:        a.cluster.ID,
		GroupPrincipalID: doubleNestedGroupPrincipalID,
		RoleTemplateID:   rbac.ClusterOwner.String(),
	}

	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", groupCRTB.GroupPrincipalID, groupCRTB.RoleTemplateID)
	_, err := authAdmin.Management.ClusterRoleTemplateBinding.Create(groupCRTB)
	require.NoError(a.T(), err, "Error occurred while creating cluster role template binding")

	for _, v := range a.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		userClient, err := login(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err)

		newUserClient, err := userClient.ReLogin()
		require.NoError(a.T(), err)

		clusterList, err := newUserClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NoError(a.T(), err)

		logrus.Infof("User [%v] lists [%v] clusters while expecting [%v] clusters", v.Username, len(clusterList.Data), 1)
		assert.Equalf(a.T(), 1, len(clusterList.Data), "Error occurred while: user [%v] lists [%v] clusters while expecting [%v] clusters to be listed", v.Username, len(clusterList.Data), 1)
	}

	for _, v := range slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers) {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		userClient, err := login(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err)

		logrus.Infof("Verifying user [%v] should NOT list clusters", v.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)

		if err == nil {
			logrus.WithField("username", v.Username).Warn("User was able to list clusters when they shouldn't")
		} else {
			logrus.WithField("username", v.Username).Info("User correctly cannot list clusters")
		}

		assert.NotNilf(a.T(), err, "Error should contain error message", "Error occurred while: user [%v] should NOT lists clusters", v.Username)
		assert.Containsf(a.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Error occurred while: user [%v] should NOT lists clusters", v.Username)
	}
	logrus.Info("Verifying cluster role template binding was created")
	crtbList, err := authAdmin.Management.ClusterRoleTemplateBinding.List(nil)
	require.NoError(a.T(), err)

	var foundCRTB *v3.ClusterRoleTemplateBinding
	for _, crtb := range crtbList.Data {
		if crtb.GroupPrincipalID == doubleNestedGroupPrincipalID && crtb.ClusterID == a.cluster.ID {
			foundCRTB = &crtb
			break
		}
	}

	assert.NotNilf(a.T(), foundCRTB, "Cluster role template binding should be found for group [%v]", doubleNestedGroupPrincipalID)

	logrus.Info("Creating project with admin")
	projectTemplate := projects.NewProjectConfig(a.cluster.ID)
	projectResp, err := authAdmin.Management.Project.Create(projectTemplate)
	require.NoError(a.T(), err)

	assert.NotNilf(a.T(), projectResp, "Error occurred while: project is created with the admin")

	nestedGroupPrincipalID := newPrincipalID(authConfigID, "group", a.authConfig.NestedGroup, searchBase)
	groupPRTB := &v3.ProjectRoleTemplateBinding{
		ProjectID:        projectResp.ID,
		GroupPrincipalID: nestedGroupPrincipalID,
		RoleTemplateID:   rbac.ProjectOwner.String(),
	}

	logrus.Infof("Creating PRTB for group [%v] with principal ID [%v]", a.authConfig.NestedGroup, nestedGroupPrincipalID)

	groupPRTBResp, err := authAdmin.Management.ProjectRoleTemplateBinding.Create(groupPRTB)
	require.NoError(a.T(), err)

	assert.NotNilf(a.T(), groupPRTBResp, "Error occurred while: creating a  PRTB for group [%v]", a.authConfig.NestedGroup)

	for _, v := range a.authConfig.NestedUsers {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		userClient, err := login(authAdmin, auth.OpenLDAPAuth, user)
		require.NoError(a.T(), err)

		namespaceName := namegen.AppendRandomString("testns-")
		namespace, err := namespaces.CreateNamespace(userClient, namespaceName, "{}", nil, nil, projectResp)
		require.NoError(a.T(), err)

		logrus.Infof("User [%v] created namespace [%v]", v.Username, namespaceName)
		assert.Equal(a.T(), namespaceName, namespace.Name, "Error occurred while: user [%v] has created namespace [%v]", v.Username, namespaceName)
	}
}

func (a *OpenLDAPAuthProviderSuite) TestRestrictedUsersAndGroupsAccessMode() {
	subSession, authAdmin := a.setupAuthenticatedTest()
	defer subSession.Cleanup()

	authConfigID := "openldap"
	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	groupPrincipalID := newPrincipalID(authConfigID, "group", a.authConfig.Group, searchBase)
	groupCRTB := &v3.ClusterRoleTemplateBinding{
		ClusterID:        a.cluster.ID,
		GroupPrincipalID: groupPrincipalID,
		RoleTemplateID:   rbac.ClusterMember.String(),
	}

	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", groupCRTB.GroupPrincipalID, groupCRTB.RoleTemplateID)

	_, err := authAdmin.Management.ClusterRoleTemplateBinding.Create(groupCRTB)
	require.NoError(a.T(), err, "Error occurred while creating cluster role template binding")

	defaultProject, err := projects.GetProjectByName(authAdmin, a.cluster.ID, "Default")
	require.NoError(a.T(), err)

	for _, v := range a.authConfig.NestedUsers {
		nestedUserPrincipalID := newPrincipalID(authConfigID, "user", v.Username, searchBase)

		userPRTB := &v3.ProjectRoleTemplateBinding{
			ProjectID:        defaultProject.ID,
			GroupPrincipalID: nestedUserPrincipalID,
			RoleTemplateID:   rbac.ProjectOwner.String(),
		}

		logrus.Infof("Creating project role template binding for user [%v] with role [%v]", userPRTB.GroupPrincipalID, userPRTB.RoleTemplateID)
		userPRTBResp, err := authAdmin.Management.ProjectRoleTemplateBinding.Create(userPRTB)
		require.NoError(a.T(), err)

		logrus.Infof("Project role template binding created for user [%v]", v.Username)
		assert.NotNilf(a.T(), userPRTBResp, "Error occurred while: project role template binding is created for user [%v]", v.Username)
	}

	var principalIDs []string

	principalIDs = append(principalIDs, newPrincipalID(authConfigID, "group", a.authConfig.DoubleNestedGroup, searchBase))
	for _, v := range a.authConfig.DoubleNestedUsers {
		principalIDs = append(principalIDs, newPrincipalID(authConfigID, "user", v.Username, searchBase))
	}

	logrus.Info("Updating access mode to restrict access to only authorized users & groups")

	existing, updates := newWithAccessMode(a.T(), authAdmin, authConfigID, "required", principalIDs)
	newAuthConfig, err := a.client.Auth.OLDAP.Update(existing, updates)
	require.NoError(a.T(), err)

	assert.Equal(a.T(), existing.AccessMode, newAuthConfig.AccessMode, "Error occurred while: access mode updated to restrict access to only the authorized users & groups")

	for _, v := range a.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		_, err := login(authAdmin, auth.OpenLDAPAuth, user)

		logrus.Infof("Verifying double nested user [%v] can login in restricted mode", v.Username)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"username": v.Username,
				"error":    err.Error(),
			}).Error("Double nested user failed to login in restricted mode")
		}
		assert.NoErrorf(a.T(), err, "Error occurred while: logging as user [%v], should be able to login", v.Username)

	}

	for _, v := range slices.Concat(a.authConfig.Users, a.authConfig.NestedUsers) {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		_, err := login(authAdmin, auth.OpenLDAPAuth, user)

		logrus.Infof("Verifying user [%v] cannot login in restricted mode", v.Username)
		if err == nil {
			logrus.WithField("username", v.Username).Warn("User was able to login when they shouldn't in restricted mode")
		}
		assert.Errorf(a.T(), err, "Error occurred while: logging as user [%v], should be able to login", v.Username)
	}

	logrus.Info("Rolling back access mode to unrestricted")

	authExisting, authWithUnrestricted := newWithAccessMode(a.T(), a.client, authConfigID, "unrestricted", nil)
	newAuthConfig, err = a.client.Auth.OLDAP.Update(authExisting, authWithUnrestricted)
	require.NoError(a.T(), err)
	assert.Equal(a.T(), authExisting.AccessMode, newAuthConfig.AccessMode, "Rolling back the access mode to unrestricted from restrict access to only the authorized users & groups")
}

func (a *OpenLDAPAuthProviderSuite) TestAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin := a.setupAuthenticatedTest()
	defer subSession.Cleanup()

	authConfigID := "openldap"
	searchBase := a.client.Auth.OLDAP.Config.Users.SearchBase

	doubleNestedGroupPrincipalID := newPrincipalID(authConfigID, "group", a.authConfig.DoubleNestedGroup, searchBase)
	groupCRTB := &v3.ClusterRoleTemplateBinding{
		ClusterID:        a.cluster.ID,
		GroupPrincipalID: doubleNestedGroupPrincipalID,
		RoleTemplateID:   rbac.ClusterMember.String(),
	}

	logrus.Infof("Creating cluster role template binding for group [%v] with role [%v]", groupCRTB.GroupPrincipalID, groupCRTB.RoleTemplateID)
	_, err := authAdmin.Management.ClusterRoleTemplateBinding.Create(groupCRTB)
	require.NoError(a.T(), err, "Error occurred while creating cluster role template binding")

	defaultProject, err := projects.GetProjectByName(authAdmin, a.cluster.ID, "Default")
	require.NoError(a.T(), err)

	for _, v := range a.authConfig.NestedUsers {
		nestedGroupPrincipalID := newPrincipalID(authConfigID, "user", v.Username, searchBase)

		userPRTB := &v3.ProjectRoleTemplateBinding{
			ProjectID:        defaultProject.ID,
			GroupPrincipalID: nestedGroupPrincipalID,
			RoleTemplateID:   rbac.ProjectOwner.String(),
		}
		userPRTBResp, err := authAdmin.Management.ProjectRoleTemplateBinding.Create(userPRTB)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"username": v.Username,
				"error":    err.Error(),
			}).Error("Failed to create project role binding")
		}
		require.NoError(a.T(), err)

		logrus.Infof("Project role binding created for user [%v]", v.Username)
		assert.NotNilf(a.T(), userPRTBResp, "Error occurred while: creating a project role binding for user [%v]", v.Username)
	}

	logrus.Info("Updating access mode to allow cluster and project members")

	authExisting, authWithRestricted := newWithAccessMode(a.T(), authAdmin, authConfigID, "restricted", nil)
	newAuthConfig, err := a.client.Auth.OLDAP.Update(authExisting, authWithRestricted)
	require.NoError(a.T(), err)

	assert.Equal(a.T(), authExisting.AccessMode, newAuthConfig.AccessMode, "Error occurred while: access mode updated to allow members of clusters and projects, plus authorized users & groups")

	for _, v := range slices.Concat(a.authConfig.DoubleNestedUsers, a.authConfig.NestedUsers) {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		_, err := login(authAdmin, auth.OpenLDAPAuth, user)

		logrus.Infof("Verifying user [%v] can login in cluster/project members mode", v.Username)
		if err != nil {
			logrus.WithFields(logrus.Fields{
				"username": v.Username,
				"error":    err.Error(),
			}).Error("User failed to login in cluster/project members mode")
		}
		assert.NoErrorf(a.T(), err, "Error occurred while: logging as user [%v], should be able to login", v.Username)
	}

	for _, v := range a.authConfig.Users {
		user := &v3.User{
			Username: v.Username,
			Password: v.Password,
		}
		_, err := login(authAdmin, auth.OpenLDAPAuth, user)

		logrus.Infof("Verifying user [%v] cannot login in cluster/project members mode", v.Username)
		if err == nil {
			logrus.WithField("username", v.Username).Warn("User was able to login when they shouldn't in cluster/project members mode")
		}
		assert.Errorf(a.T(), err, "Verifying logging as user [%v], should NOT be able to login", v.Username)
	}

	logrus.Info("Rolling back access mode to unrestricted from cluster/project members mode")

	authExisting, authWithUnrestricted := newWithAccessMode(a.T(), authAdmin, authConfigID, "unrestricted", nil)
	newAuthConfig, err = a.client.Auth.OLDAP.Update(authExisting, authWithUnrestricted)
	require.NoError(a.T(), err)
	assert.Equal(a.T(), authExisting.AccessMode, newAuthConfig.AccessMode, "Rolling back the access mode to unrestricted from allow members of clusters and projects, plus authorized users & groups")
}

func TestOpenLDAPAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(OpenLDAPAuthProviderSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package openldap

import (
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extfeaturesapi "github.com/rancher/shepherd/extensions/kubeapi/features"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rbacactions "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/features"
	"github.com/rancher/tests/actions/kubeapi/users"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	hideLocalAuthFeature = "hide-local-auth-provider"
)

type HideLocalAuthTestSuite struct {
	suite.Suite
	client     *rancher.Client
	session    *session.Session
	adminUser  *v3.User
	authConfig *authactions.AuthConfig
}

func (s *HideLocalAuthTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)
	s.client = client

	log.Info("Verifying `hide-local-auth-provider` feature flag is disabled")
	disabled, err := features.IsEnabled(s.client, hideLocalAuthFeature)
	require.NoError(s.T(), err)
	require.False(s.T(), disabled)

	log.Info("Loading auth configuration from config file")
	s.authConfig = new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, s.authConfig)
	require.NotNil(s.T(), s.authConfig, "Auth configuration is not provided")

	log.Info("Setting up admin user credentials for OpenLDAP authentication")
	s.adminUser = &v3.User{
		Username: client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: client.Auth.OLDAP.Config.Users.Admin.Password,
	}
}

func (s *HideLocalAuthTestSuite) TestHideLocalAuthEnabledWithOpenLDAP() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(s.client, s.session, s.adminUser, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	testuser1, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 1")
	log.Infof("Created local user: %s", testuser1.Username)

	testuser2, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 2")
	log.Infof("Created local user: %s", testuser2.Username)

	log.Info("Enabling OpenLDAP auth provider")
	err = authactions.EnsureAuthProviderEnabled(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to enable OpenLDAP")
	ldapConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to retrieve OpenLDAP config")
	require.True(s.T(), ldapConfig.Enabled, "OpenLDAP should be enabled")
	log.Infof("OpenLDAP is enabled: %v", ldapConfig.Enabled)

	log.Info("Enabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, true)
	require.NoError(s.T(), err)
	log.Info("Enabled 'hide-local-auth-provider' feature flag successfully")

	log.Info("Attempting to create a local user while 'hide-local-auth-provider' is enabled")
	_, err = users.CreateUser(authAdmin)
	require.Error(s.T(), err, "Local user shouldn't be created when hide-local-auth-provider is enabled")
	require.Contains(s.T(), err.Error(), "denied the request: can't create user")
	require.Contains(s.T(), err.Error(), "for disabled local provider")

	log.Info("Disabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, false)
	require.NoError(s.T(), err)
	log.Info("Disabled 'hide-local-auth-provider' feature flag successfully")
}

func (s *HideLocalAuthTestSuite) TestHideLocalAuthEnabledWithNoExternalAuthEnabled() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	testuser1, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 1")
	log.Infof("Created local user: %s", testuser1.Username)

	testuser2, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 2")
	log.Infof("Created local user: %s", testuser2.Username)

	log.Info("Enabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, true)
	require.NoError(s.T(), err)
	log.Info("Enabled 'hide-local-auth-provider' feature flag successfully")

	log.Info("Verifying local user can be created while 'hide-local-auth-provider' is enabled with no external auth provider enabled")
	_, err = users.CreateUser(s.client)
	require.NoError(s.T(), err)

	log.Info("Verifying local user can be edited while 'hide-local-auth-provider' is enabled with no external auth provider enabled")
	testuser1.DisplayName = "Updated Display Name"
	_, err = extuserapi.UpdateUser(s.client, testuser1)
	require.NoError(s.T(), err)
	require.Contains(s.T(), testuser1.DisplayName, "Updated Display Name")

	log.Info("Verifying local user can be deleted while 'hide-local-auth-provider' is enabled with no external auth provider enabled")
	err = s.client.WranglerContext.Mgmt.User().Delete(testuser2.Username, &metav1.DeleteOptions{})
	require.NoError(s.T(), err)

	log.Info("Disabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, false)
	require.NoError(s.T(), err)
	log.Info("Disabled 'hide-local-auth-provider' feature flag successfully")
}

func (s *HideLocalAuthTestSuite) TestUserWithoutManageUsersCannotEditExistingLocalUserWhileHideLocalAuthProviderIsEnabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(s.client, s.session, s.adminUser, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	testuser1, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 1")
	log.Infof("Created local user: %s", testuser1.Username)

	testuser2, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 2")
	log.Infof("Created local user: %s", testuser2.Username)

	log.Info("Enabling OpenLDAP auth provider")
	err = authactions.EnsureAuthProviderEnabled(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to enable OpenLDAP")
	ldapConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to retrieve OpenLDAP config")
	require.True(s.T(), ldapConfig.Enabled, "OpenLDAP should be enabled")
	log.Infof("OpenLDAP is enabled: %v", ldapConfig.Enabled)

	log.Info("Enabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, true)
	require.NoError(s.T(), err)
	log.Info("Enabled 'hide-local-auth-provider' feature flag successfully")

	user := &v3.User{
		Username: s.authConfig.Users[0].Username,
		Password: s.authConfig.Users[0].Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to login user [%v]", user.Username)

	log.Infof("Granting the OpenLDAP user %s permissions to update User CRDs, which does not include manage-users permissions", user.Username)
	err = rbacactions.GrantUserCRDUpdatePermissions(s.client, s.client.RancherConfig.ClusterName, userClient.UserID)
	require.NoError(s.T(), err, "Failed to grant user CRD update permissions")

	log.Infof("Attempting to edit existing local user %s while 'hide-local-auth-provider' is enabled", testuser1.Username)
	testuser1.DisplayName = "Updated Display Name"
	_, err = extuserapi.UpdateUser(userClient, testuser1)
	require.Error(s.T(), err)
	require.Contains(s.T(), err.Error(), "denied the request: can't update user")
	require.Contains(s.T(), err.Error(), "for disabled local provider")

	log.Info("Disabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, false)
	require.NoError(s.T(), err)
	log.Info("Disabled 'hide-local-auth-provider' feature flag successfully")
}

func (s *HideLocalAuthTestSuite) TestUserWithManageUsersCanEditDeleteExistingLocalUserWhileHideLocalAuthProviderIsEnabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(s.client, s.session, s.adminUser, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	testuser1, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 1")
	log.Infof("Created local user: %s", testuser1.Username)

	testuser2, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 2")
	log.Infof("Created local user: %s", testuser2.Username)

	log.Info("Enabling OpenLDAP auth provider")
	err = authactions.EnsureAuthProviderEnabled(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to enable OpenLDAP")
	ldapConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to retrieve OpenLDAP config")
	require.True(s.T(), ldapConfig.Enabled, "OpenLDAP should be enabled")
	log.Infof("OpenLDAP is enabled: %v", ldapConfig.Enabled)

	log.Info("Enabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, true)
	require.NoError(s.T(), err)
	log.Info("Enabled 'hide-local-auth-provider' feature flag successfully")

	user := &v3.User{
		Username: s.authConfig.Users[0].Username,
		Password: s.authConfig.Users[0].Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to login user [%v]", user.Username)

	log.Infof("Granting manage-users role to user %s", user.Username)
	manageUsersBinding := &managementv3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "users-manage-",
		},
		GlobalRoleName: "users-manage",
		UserName:       userClient.UserID,
	}
	_, err = authAdmin.WranglerContext.Mgmt.GlobalRoleBinding().Create(manageUsersBinding)
	require.NoError(s.T(), err, "Failed to create manage-users role binding")

	log.Infof("Attempting to edit existing local user %s while 'hide-local-auth-provider' is enabled", testuser1.Username)
	testuser1.DisplayName = "Updated Display Name"
	_, err = extuserapi.UpdateUser(userClient, testuser1)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "Updated Display Name", testuser1.DisplayName)

	log.Info("Disabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, false)
	require.NoError(s.T(), err)
	log.Info("Disabled 'hide-local-auth-provider' feature flag successfully")
}

func (s *HideLocalAuthTestSuite) TearDownSuite() {
	log.Infof("Disabling the feature flag %s", hideLocalAuthFeature)
	err := extfeaturesapi.DisableFeatureFlag(s.client, hideLocalAuthFeature)
	if err != nil {
		log.Warnf("Failed to disable the feature flag during teardown: %v", err)
	}

	if s.client != nil {
		ldapConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
		if err == nil && ldapConfig.Enabled {
			log.Info("Disabling OpenLDAP authentication after test suite")
			err := s.client.Auth.OLDAP.Disable()
			require.NoError(s.T(), err, "Failed to disable OpenLDAP in teardown")
		}
	}
}

func (s *HideLocalAuthTestSuite) TestLocalAuthWorksAfterDisablingExternalAuthProvider() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	testuser1, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 1")
	log.Infof("Created local user: %s", testuser1.Username)

	testuser2, err := users.CreateUser(s.client)
	require.NoError(s.T(), err, "Failed to create test user 2")
	log.Infof("Created local user: %s", testuser2.Username)

	log.Info("Enabling hide-local-auth-provider feature flag")
	err = features.UpdateFeatureFlag(s.client, hideLocalAuthFeature, true)
	require.NoError(s.T(), err)
	log.Info("Enabled 'hide-local-auth-provider' feature flag successfully")

	log.Info("Enabling OpenLDAP auth provider")
	err = authactions.EnsureAuthProviderEnabled(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to enable OpenLDAP")
	ldapConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to retrieve OpenLDAP config")
	require.True(s.T(), ldapConfig.Enabled, "OpenLDAP should be enabled")
	log.Infof("OpenLDAP is enabled: %v", ldapConfig.Enabled)

	log.Info("Disabling OpenLDAP auth provider")
	err = s.client.Auth.OLDAP.Disable()
	require.NoError(s.T(), err, "Failed to disable OpenLDAP")

	log.Info("Verifying local user can be created while 'hide-local-auth-provider' is enabled with no external auth provider enabled")
	_, err = users.CreateUser(s.client)
	require.NoError(s.T(), err)

	log.Info("Verifying local user can be edited while 'hide-local-auth-provider' is enabled with no external auth provider enabled")
	testuser1.DisplayName = "Updated Display Name"
	_, err = extuserapi.UpdateUser(s.client, testuser1)
	require.NoError(s.T(), err)
	require.Contains(s.T(), testuser1.DisplayName, "Updated Display Name")

	log.Info("Verifying local user can be deleted while 'hide-local-auth-provider' is enabled with no external auth provider enabled")
	err = s.client.WranglerContext.Mgmt.User().Delete(testuser2.Username, &metav1.DeleteOptions{})
	require.NoError(s.T(), err)

	log.Infof("Disabling the feature flag %s", hideLocalAuthFeature)
	err = extfeaturesapi.DisableFeatureFlag(s.client, hideLocalAuthFeature)
	require.NoError(s.T(), err)
}

func TestHideLocalAuthTestSuite(t *testing.T) {
	suite.Run(t, new(HideLocalAuthTestSuite))
}

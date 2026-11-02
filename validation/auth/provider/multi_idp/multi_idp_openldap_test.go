//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package multiidp

import (
	"slices"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type MultiOpenLDAPSuite struct {
	suite.Suite
	session   *session.Session
	client    *rancher.Client
	instances []authactions.NamedOpenLdapInstance
}

func (m *MultiOpenLDAPSuite) SetupSuite() {
	m.session = session.NewSession()

	client, err := rancher.NewClient("", m.session)
	require.NoError(m.T(), err, "Failed to create Rancher client")
	m.client = client

	m.instances, err = LoadInstances()
	require.NoError(m.T(), err, "Failed to load the multi IdP instances")
}

func (m *MultiOpenLDAPSuite) TearDownSuite() {
	m.session.Cleanup()

	for _, err := range TearDownInstances(m.client, m.instances) {
		require.NoError(m.T(), err, "Failed to remove an auth config in teardown")
	}
}

func (m *MultiOpenLDAPSuite) secretExists(configName string) (bool, error) {
	secretID := authactions.NamedConfigSecretID(configName, authactions.OpenLdap, authactions.OpenLdapConfigType, authactions.OpenLdapSecretField)

	_, err := extsecretapi.GetSecretByName(m.client, extclusterapi.LocalCluster, rbac.GlobalDataNS, secretID)
	if apierrors.IsNotFound(err) {
		return false, nil
	}

	return err == nil, err
}

func (m *MultiOpenLDAPSuite) verifyEnabled(instance *authactions.NamedOpenLdapInstance) {
	require.NoError(m.T(), authactions.EnsureNamedOpenLdapEnabled(m.client, instance), "Failed to enable %s", instance.ConfigName)

	authConfig, err := m.client.Management.AuthConfig.ByID(instance.ConfigName)
	require.NoError(m.T(), err, "Failed to retrieve auth config %s", instance.ConfigName)

	require.True(m.T(), authConfig.Enabled, "%s should be enabled", instance.ConfigName)
	require.Equal(m.T(), authactions.OpenLdapConfigType, authConfig.Type, "%s should be an OpenLDAP config", instance.ConfigName)
	require.Equal(m.T(), instance.DisplayName, authConfig.DisplayName, "%s should keep its display name", instance.ConfigName)
	require.Equal(m.T(), authactions.AuthProvCleanupAnnotationValUnlocked, authConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "%s annotation should be unlocked", instance.ConfigName)

	exists, err := m.secretExists(instance.ConfigName)
	require.NoError(m.T(), err, "Failed to look up the password secret of %s", instance.ConfigName)
	require.True(m.T(), exists, "%s should have its own password secret", instance.ConfigName)
}

func (m *MultiOpenLDAPSuite) TestEnableTwoConfigs() {
	first, second := &m.instances[0], &m.instances[1]

	m.verifyEnabled(first)
	m.verifyEnabled(second)

	firstSecret, err := extsecretapi.GetSecretByName(m.client, extclusterapi.LocalCluster, rbac.GlobalDataNS,
		authactions.NamedConfigSecretID(first.ConfigName, authactions.OpenLdap, authactions.OpenLdapConfigType, authactions.OpenLdapSecretField))
	require.NoError(m.T(), err, "Failed to retrieve the password secret of %s", first.ConfigName)

	secondSecret, err := extsecretapi.GetSecretByName(m.client, extclusterapi.LocalCluster, rbac.GlobalDataNS,
		authactions.NamedConfigSecretID(second.ConfigName, authactions.OpenLdap, authactions.OpenLdapConfigType, authactions.OpenLdapSecretField))
	require.NoError(m.T(), err, "Failed to retrieve the password secret of %s", second.ConfigName)

	require.Equal(m.T(), first.OpenLdap.ServiceAccount.Password, string(firstSecret.Data[authactions.OpenLdapSecretField]), "%s secret should hold its own password", first.ConfigName)
	require.Equal(m.T(), second.OpenLdap.ServiceAccount.Password, string(secondSecret.Data[authactions.OpenLdapSecretField]), "%s secret should hold its own password", second.ConfigName)
}

func (m *MultiOpenLDAPSuite) TestEnableConfiguredInstancesDynamic() {
	for i := range m.instances {
		logrus.Infof("Enabling auth config %s", m.instances[i].ConfigName)
		m.verifyEnabled(&m.instances[i])
	}
}

func (m *MultiOpenLDAPSuite) verifyLoginPrincipal(authAdmin *rancher.Client, instance *authactions.NamedOpenLdapInstance, userInfo authactions.User) {
	userClient, err := LoginNamedUser(authAdmin, instance, userInfo)
	require.NoError(m.T(), err, "User [%v] should be able to login through %s", userInfo.Username, instance.ConfigName)

	loggedIn, err := m.client.Management.User.ByID(userClient.UserID)
	require.NoError(m.T(), err, "Failed to retrieve the user [%v] logged in as", userInfo.Username)

	require.True(m.T(), slices.ContainsFunc(loggedIn.PrincipalIDs, func(id string) bool {
		return strings.HasPrefix(id, instance.PrincipalPrefix())
	}), "User [%v] should hold a principal starting with %s, got %v", userInfo.Username, instance.PrincipalPrefix(), loggedIn.PrincipalIDs)
}

func (m *MultiOpenLDAPSuite) TestLoginPerConfig() {
	for i := range m.instances[:minimumInstances] {
		m.verifyFirstUserLogin(&m.instances[i])
	}
}

func (m *MultiOpenLDAPSuite) verifyFirstUserLogin(instance *authactions.NamedOpenLdapInstance) {
	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, instance)
	require.NoError(m.T(), err, "Failed to setup authenticated test for %s", instance.ConfigName)
	defer subSession.Cleanup()

	require.NotEmpty(m.T(), instance.Directory.Users, "%s needs users to sign in as", instance.ConfigName)
	m.verifyLoginPrincipal(authAdmin, instance, instance.Directory.Users[0])
}

func (m *MultiOpenLDAPSuite) TestLoginUsersDynamic() {
	for i := range m.instances {
		m.verifyAllUserLogins(&m.instances[i])
	}
}

func (m *MultiOpenLDAPSuite) verifyAllUserLogins(instance *authactions.NamedOpenLdapInstance) {
	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, instance)
	require.NoError(m.T(), err, "Failed to setup authenticated test for %s", instance.ConfigName)
	defer subSession.Cleanup()

	for _, userInfo := range instance.Directory.Users {
		m.verifyLoginPrincipal(authAdmin, instance, userInfo)
	}
}

func (m *MultiOpenLDAPSuite) TestCrossConfigLoginDenied() {
	first, second := &m.instances[0], &m.instances[1]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, first)
	require.NoError(m.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NoError(m.T(), authactions.EnsureNamedOpenLdapEnabled(m.client, second), "Failed to enable %s", second.ConfigName)

	// A username present in both directories can legitimately sign in through either config.
	secondOnly := slices.DeleteFunc(slices.Clone(second.Directory.Users), func(user authactions.User) bool {
		return slices.ContainsFunc(first.Directory.Users, func(other authactions.User) bool { return other.Username == user.Username })
	})
	require.NotEmpty(m.T(), secondOnly, "%s needs users that do not exist in %s", second.ConfigName, first.ConfigName)

	err = authactions.VerifyNamedUserLogins(authAdmin, first.ConfigName, secondOnly, "user of another directory", false)
	require.NoError(m.T(), err, "Users of %s must not sign in through %s", second.ConfigName, first.ConfigName)
}

func (m *MultiOpenLDAPSuite) TestUserAttributeKeyedByConfig() {
	for i := range m.instances[:minimumInstances] {
		m.verifyUserAttributeKeyed(&m.instances[i])
	}
}

func (m *MultiOpenLDAPSuite) verifyUserAttributeKeyed(instance *authactions.NamedOpenLdapInstance) {
	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, instance)
	require.NoError(m.T(), err, "Failed to setup authenticated test for %s", instance.ConfigName)
	defer subSession.Cleanup()

	require.NotEmpty(m.T(), instance.Directory.Users, "%s needs users to sign in as", instance.ConfigName)
	userInfo := instance.Directory.Users[0]

	userClient, err := LoginNamedUser(authAdmin, instance, userInfo)
	require.NoError(m.T(), err, "Failed to login user [%v]", userInfo.Username)

	groupKeys, extraKeys, err := authactions.UserAttributeConfigKeys(m.client, userClient.UserID)
	require.NoError(m.T(), err, "Failed to read the user attribute")

	require.Contains(m.T(), groupKeys, instance.ConfigName, "Groups should be stored under the auth config name")
	require.Contains(m.T(), extraKeys, instance.ConfigName, "Extras should be stored under the auth config name")
	// The user attribute is shared by every config the user has signed in through, so the provider key can hold entries from the default config. Only this config's entries are a failure.
	groupIDs, extraIDs, err := authactions.UserAttributeEntriesUnderKey(m.client, userClient.UserID, authactions.OpenLdap)
	require.NoError(m.T(), err, "Failed to read the user attribute entries")
	require.False(m.T(), slices.ContainsFunc(append(groupIDs, extraIDs...), func(id string) bool {
		return strings.HasPrefix(id, instance.ConfigName+"_")
	}), "Principals of %s must not be stored under the provider name", instance.ConfigName)
}

func (m *MultiOpenLDAPSuite) TestAccessModePerConfig() {
	restrictedInstance, openInstance := &m.instances[0], &m.instances[1]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, restrictedInstance)
	require.NoError(m.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NoError(m.T(), authactions.EnsureNamedOpenLdapEnabled(m.client, openInstance), "Failed to enable %s", openInstance.ConfigName)

	require.GreaterOrEqual(m.T(), len(restrictedInstance.Directory.Users), 2, "%s needs two users to tell allowed from denied", restrictedInstance.ConfigName)
	allowedUser, deniedUsers := restrictedInstance.Directory.Users[0], restrictedInstance.Directory.Users[1:]

	allowedIDs := []string{
		restrictedInstance.UserPrincipalID(allowedUser.Username),
		restrictedInstance.UserPrincipalID(restrictedInstance.OpenLdap.Users.Admin.Username),
	}

	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateNamedAccessMode(m.client, restrictedInstance.ConfigName, authactions.AccessModeUnrestricted, nil)

		return err
	})

	updated, err := authactions.UpdateNamedAccessMode(m.client, restrictedInstance.ConfigName, authactions.AccessModeRestricted, allowedIDs)
	require.NoError(m.T(), err, "Failed to restrict %s", restrictedInstance.ConfigName)
	require.Equal(m.T(), authactions.AccessModeRestricted, updated.AccessMode, "%s should be restricted", restrictedInstance.ConfigName)

	require.NoError(m.T(), authactions.VerifyNamedUserLogins(authAdmin, restrictedInstance.ConfigName, []authactions.User{allowedUser}, "allowed user of restricted config", true))
	require.NoError(m.T(), authactions.VerifyNamedUserLogins(authAdmin, restrictedInstance.ConfigName, deniedUsers, "unlisted users of restricted config", false))

	openConfig, err := m.client.Management.AuthConfig.ByID(openInstance.ConfigName)
	require.NoError(m.T(), err, "Failed to retrieve %s", openInstance.ConfigName)
	require.Equal(m.T(), authactions.AccessModeUnrestricted, openConfig.AccessMode, "Restricting %s must not change %s", restrictedInstance.ConfigName, openInstance.ConfigName)

	require.NoError(m.T(), authactions.VerifyNamedUserLogins(authAdmin, openInstance.ConfigName, openInstance.Directory.Users, "users of the unrestricted config", true))
}

func (m *MultiOpenLDAPSuite) TestDisableOneConfigLeavesOthers() {
	disabled, kept := &m.instances[0], &m.instances[1]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, kept)
	require.NoError(m.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NoError(m.T(), authactions.EnsureNamedOpenLdapEnabled(m.client, disabled), "Failed to enable %s", disabled.ConfigName)

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureNamedOpenLdapEnabled(m.client, disabled)
	})

	require.NoError(m.T(), authactions.DisableNamedOpenLdap(m.client, disabled), "Failed to disable %s", disabled.ConfigName)

	disabledConfig, err := m.client.Management.AuthConfig.ByID(disabled.ConfigName)
	require.NoError(m.T(), err, "Failed to retrieve %s", disabled.ConfigName)
	require.False(m.T(), disabledConfig.Enabled, "%s should be disabled", disabled.ConfigName)
	require.Equal(m.T(), authactions.AuthProvCleanupAnnotationValLocked, disabledConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "%s annotation should be locked", disabled.ConfigName)

	exists, err := m.secretExists(disabled.ConfigName)
	require.NoError(m.T(), err, "Failed to look up the password secret of %s", disabled.ConfigName)
	require.False(m.T(), exists, "Disabling %s should remove its password secret", disabled.ConfigName)

	exists, err = m.secretExists(kept.ConfigName)
	require.NoError(m.T(), err, "Failed to look up the password secret of %s", kept.ConfigName)
	require.True(m.T(), exists, "Disabling %s must keep the password secret of %s", disabled.ConfigName, kept.ConfigName)

	keptConfig, err := m.client.Management.AuthConfig.ByID(kept.ConfigName)
	require.NoError(m.T(), err, "Failed to retrieve %s", kept.ConfigName)
	require.True(m.T(), keptConfig.Enabled, "%s should stay enabled", kept.ConfigName)

	require.NoError(m.T(), authactions.VerifyNamedUserLogins(authAdmin, kept.ConfigName, kept.Directory.Users, "users of the config that stayed enabled", true))
	require.NotEmpty(m.T(), disabled.Directory.Users, "%s needs users to sign in as", disabled.ConfigName)
	require.NoError(m.T(), authactions.VerifyNamedUserLogins(authAdmin, disabled.ConfigName, disabled.Directory.Users[:1], "user of the disabled config", false))
}

func (m *MultiOpenLDAPSuite) TestDeletedConfigStateIsRefreshedAway() {
	deleted, kept := &m.instances[0], &m.instances[1]

	subSession, _, err := authactions.SetupNamedOpenLdapSession(m.client, m.session, kept)
	require.NoError(m.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NoError(m.T(), authactions.EnsureNamedOpenLdapEnabled(m.client, deleted), "Failed to enable %s", deleted.ConfigName)

	adminID := m.client.UserID

	groupKeys, _, err := authactions.UserAttributeConfigKeys(m.client, adminID)
	require.NoError(m.T(), err, "Failed to read the user attribute")
	require.Contains(m.T(), groupKeys, deleted.ConfigName, "Enabling should have stored groups under %s", deleted.ConfigName)

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureNamedOpenLdapEnabled(m.client, deleted)
	})

	require.NoError(m.T(), authactions.DeleteNamedOpenLdap(m.client, deleted), "Failed to delete %s", deleted.ConfigName)

	require.NoError(m.T(), authactions.WaitForAuthConfigDeleted(m.client, deleted.ConfigName), "Failed to wait for %s to be deleted", deleted.ConfigName)

	require.NoError(m.T(), authactions.WaitForUserAttributeConfigKeyRemoved(m.client, adminID, deleted.ConfigName),
		"The refresh should drop what the deleted config stored for the administrator")
}

func TestMultiOpenLDAPSuite(t *testing.T) {
	suite.Run(t, new(MultiOpenLDAPSuite))
}

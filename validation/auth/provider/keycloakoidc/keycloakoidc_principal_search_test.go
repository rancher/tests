//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package keycloakoidc

import (
	"slices"

	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchFindsGroups() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	logrus.Infof("Searching principals for Keycloak group [%v]", groupName)
	expectedPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedPrincipalID)
	require.NoError(k.T(), err, "Group [%v] should be returned by principal search", groupName)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchFindsUsers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(k.authConfig.Users, k.authConfig.ExcludedUsers)
	for _, userInfo := range allUsers {
		logrus.Infof("Searching principals for Keycloak user [%v]", userInfo.Username)
		expectedPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, userInfo)

		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, userInfo.Username, expectedPrincipalID)
		require.NoError(k.T(), err, "User [%v] should be returned by principal search carrying the subject claim its principal is built from", userInfo.Username)
	}
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchByPrincipalType() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	expectedGroupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, groupName)

	logrus.Infof("Searching principals for Keycloak group [%v] restricted to type [%v]", groupName, authactions.PrincipalTypeGroup)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, groupName, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(k.T(), err, "Search restricted to groups should return group [%v] and nothing of another type", groupName)

	userInfo := k.authConfig.Users[0]
	expectedUserPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, userInfo)

	logrus.Infof("Searching principals for Keycloak user [%v] restricted to type [%v]", userInfo.Username, authactions.PrincipalTypeUser)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, userInfo.Username, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(k.T(), err, "Search restricted to users should return user [%v] and nothing of another type", userInfo.Username)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchByPartialName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	require.NotEmpty(k.T(), groupName, "Group name should be set in the auth configuration")
	groupPrefix := groupName[:len(groupName)/2+1]

	logrus.Infof("Searching principals for Keycloak group [%v] using partial name [%v]", groupName, groupPrefix)
	fullPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, fullPrincipalID)
	require.NoError(k.T(), err, "Keycloak OIDC answers a principal search from the realm rather than from the term, so partial name [%v] should resolve to group [%v]", groupPrefix, groupName)

	prefixPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, groupPrefix)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, prefixPrincipalID)
	require.Error(k.T(), err, "The search term itself names no group in the realm, so principal [%v] must not come back; a search result here is evidence a principal exists", prefixPrincipalID)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchRejectsUnknownNames() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	unknownName := "keycloak-oidc-no-such-principal"
	unexpectedPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, authactions.User{Username: unknownName})

	logrus.Infof("Searching principals for [%v], which exists nowhere in the Keycloak realm", unknownName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, unknownName, unexpectedPrincipalID)
	require.Error(k.T(), err, "A Keycloak OIDC principal search reaches the realm, so [%v] must come back empty rather than be echoed as a principal of its own", unknownName)

	err = authactions.VerifyPrincipalSearchExcludesProvider(k.client, unknownName, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "No Keycloak principal of any kind should be returned for a name the realm does not hold")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalByIDResolvesGroupAndUser() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Resolving Keycloak group principal [%v] by ID", groupPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.KeycloakOIDC, authactions.PrincipalTypeGroup)
	require.NoError(k.T(), err, "Group principal [%v] should resolve by ID", groupPrincipalID)

	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, k.authConfig.Users[0])

	logrus.Infof("Resolving Keycloak user principal [%v] by ID", userPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.KeycloakOIDC, authactions.PrincipalTypeUser)
	require.NoError(k.T(), err, "User principal [%v] should resolve by ID", userPrincipalID)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchProvisionedUserIsNotLocal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userInfo := k.authConfig.Users[0]
	logrus.Infof("Logging in as Keycloak user [%v] to provision the Rancher user", userInfo.Username)
	user := &v3.User{
		Username: userInfo.Username,
		Password: userInfo.Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

	provisionedUser, err := k.client.WranglerContext.Mgmt.User().Get(userClient.UserID, metav1.GetOptions{})
	require.NoError(k.T(), err, "Failed to retrieve the Rancher user provisioned for [%v]", userInfo.Username)
	logrus.Infof("Keycloak user [%v] provisioned Rancher user [%v] with display name [%v], username [%v] and principals %v",
		userInfo.Username, provisionedUser.Name, provisionedUser.DisplayName, provisionedUser.Username, provisionedUser.PrincipalIDs)

	require.Empty(k.T(), provisionedUser.Username, "Externally provisioned user [%v] should carry no local login username", userInfo.Username)
	require.Greater(k.T(), len(provisionedUser.PrincipalIDs), 1, "Externally provisioned user [%v] should carry an external principal alongside the local one", userInfo.Username)

	expectedPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, userInfo)

	logrus.Infof("Searching principals for provisioned user [%v]", userInfo.Username)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, userInfo.Username, expectedPrincipalID)
	require.NoError(k.T(), err, "Provisioned user [%v] should stay findable in the realm", userInfo.Username)

	err = authactions.VerifyPrincipalNotLocal(authAdmin, userInfo.Username)
	require.NoError(k.T(), err, "Provisioned user [%v] should not surface as a local principal", userInfo.Username)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchAfterProviderDisabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	expectedGroupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, groupName)

	logrus.Infof("Confirming Keycloak group [%v] is returned while the provider is enabled", groupName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedGroupPrincipalID)
	require.NoError(k.T(), err, "Group [%v] should be returned while Keycloak OIDC is enabled", groupName)

	logrus.Info("Disabling Keycloak OIDC before searching principals again")
	err = k.client.Auth.KeycloakOIDC.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak OIDC")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	logrus.Info("Waiting for the OpenID Connect session that found the group to stop being accepted")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(k.T(), err, "Disabling the provider should delete the tokens it issued, so the session that reached the Keycloak principals should stop working")

	logrus.Infof("Searching principals for [%v] with Keycloak OIDC disabled", groupName)
	err = authactions.VerifyPrincipalSearchExcludesProvider(k.client, groupName, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "No Keycloak principal should be returned while the provider is disabled")
}

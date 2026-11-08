//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package keycloakoidc

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
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

type KeycloakOIDCAuthProviderSuite struct {
	suite.Suite
	session          *session.Session
	keycloakSession  *session.Session
	client           *rancher.Client
	keycloak         *keycloak.Client
	cluster          *v3.Cluster
	adminUser        *v3.User
	adminPrincipalID string
	clientID         string
	authConfig       *authactions.ExternalAuthConfig
}

func (k *KeycloakOIDCAuthProviderSuite) SetupSuite() {
	k.session = session.NewSession()
	k.keycloakSession = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err, "Failed to create Rancher client")
	k.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(k.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(k.client, clusterName)
	require.NoError(k.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	k.cluster, err = k.client.Management.Cluster.ByID(clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Connecting to Keycloak as a realm administrator")
	k.keycloak, err = authactions.NewKeycloakOIDCClient(k.keycloakSession)
	require.NoError(k.T(), err, "Failed to create Keycloak admin client")

	logrus.Info("Registering the Rancher OpenID Connect client and settling the test accounts in the Keycloak realm")
	keycloakOIDCFixture, err := authactions.SetupKeycloakOIDC(k.client, k.keycloak)
	require.NoError(k.T(), err, "Failed to set up the Keycloak realm for Keycloak OIDC")

	k.authConfig = keycloakOIDCFixture.AuthInput
	require.NotEmpty(k.T(), k.authConfig.Group, "Keycloak OIDC setup should have settled on the allowed group")
	require.NotEmpty(k.T(), k.authConfig.Users, "Keycloak OIDC setup should have settled on the users to sign in as")
	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC setup should have settled on a user outside the allowed group")
	require.NotEmpty(k.T(), k.authConfig.NestedGroup, "Keycloak OIDC setup should have settled on a group nested beneath the allowed group")
	require.NotEmpty(k.T(), k.authConfig.NestedUsers, "Keycloak OIDC setup should have settled on a member of the nested group")
	require.NotEmpty(k.T(), k.authConfig.DoubleNestedGroup, "Keycloak OIDC setup should have settled on a group nested two deep")
	require.NotEmpty(k.T(), k.authConfig.DoubleNestedUsers, "Keycloak OIDC setup should have settled on a member of the doubly nested group")

	k.adminUser = &v3.User{
		Username: keycloakOIDCFixture.Admin.Username,
		Password: keycloakOIDCFixture.Admin.Password,
	}
	k.adminPrincipalID = keycloakOIDCFixture.AdminPrincipalID
	k.clientID = keycloakOIDCFixture.ClientID
}

func (k *KeycloakOIDCAuthProviderSuite) TearDownSuite() {
	defer k.keycloakSession.Cleanup()
	defer k.session.Cleanup()

	if k.client != nil {
		keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakOIDC)
		if err == nil && keycloakConfig.Enabled {
			logrus.Info("Disabling Keycloak OIDC authentication after test suite")
			err := k.client.Auth.KeycloakOIDC.Disable()
			require.NoError(k.T(), err, "Failed to disable Keycloak OIDC in teardown")
		}
	}
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCEnableProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak OIDC config")

	require.True(k.T(), keycloakConfig.Enabled, "Keycloak OIDC should be enabled")
	require.Equal(k.T(), v3.KeyCloakOIDCConfigType, keycloakConfig.Type, "Auth config should be stored as the Keycloak OIDC subtype")
	require.Equal(k.T(), authactions.AuthProvCleanupAnnotationValUnlocked, keycloakConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := extsecretapi.GetSecretByName(
		k.client,
		extclusterapi.LocalCluster,
		rbac.GlobalDataNS,
		authactions.KeycloakOIDCClientSecretID,
	)
	require.NoError(k.T(), err, "Rancher should move the client secret out of the auth config into a secret")
	require.NotEmpty(k.T(), secret.Data, "Client secret secret should hold the secret")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCDisableAndReenableProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	err = k.client.Auth.KeycloakOIDC.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak OIDC")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	keycloakConfig, err := authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	require.False(k.T(), keycloakConfig.Enabled, "Keycloak OIDC should be disabled")
	require.Equal(k.T(), authactions.AuthProvCleanupAnnotationValLocked, keycloakConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be locked")

	_, err = extsecretapi.GetSecretByName(
		k.client,
		extclusterapi.LocalCluster,
		rbac.GlobalDataNS,
		authactions.KeycloakOIDCClientSecretID,
	)
	require.Error(k.T(), err, "Disabling the provider should remove the secret holding the client secret, so a disabled provider leaves no credential behind")
	require.True(k.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)

	err = authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to re-enable Keycloak OIDC")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCConfigureTestReturnsAuthorizationRequest() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	keycloakOIDCConfig := k.client.Auth.KeycloakOIDC.Config

	redirectURL, err := k.client.Auth.KeycloakOIDC.ConfigureTest()
	require.NoError(k.T(), err, "Rancher should answer configureTest with the address the administrator signs in at")

	authorizationRequest, err := url.Parse(redirectURL)
	require.NoError(k.T(), err, "configureTest should answer with a parseable address, got [%v]", redirectURL)

	require.True(k.T(), strings.HasPrefix(redirectURL, keycloakOIDCConfig.AuthEndpoint),
		"configureTest should send the administrator to the configured authorization endpoint [%v], got [%v]",
		keycloakOIDCConfig.AuthEndpoint, redirectURL)

	parameters := authorizationRequest.Query()
	require.Equal(k.T(), keycloakOIDCConfig.ClientID, parameters.Get("client_id"), "The authorization request should name the registered client")
	require.Equal(k.T(), "code", parameters.Get("response_type"), "The authorization request should ask for an authorization code")
	require.Equal(k.T(), keycloakOIDCConfig.RancherURL, parameters.Get("redirect_uri"), "The authorization request should come back to the configured Rancher address")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCEnableIntoRestrictedAccessModeIsNotGuarded() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	configuredAccessMode := k.client.Auth.KeycloakOIDC.Config.AccessMode
	configuredPrincipalIDs := slices.Clone(k.client.Auth.KeycloakOIDC.Config.AllowedPrincipalIDs)
	subSession.RegisterCleanupFunc(func() error {
		k.client.Auth.KeycloakOIDC.Config.AccessMode = configuredAccessMode
		k.client.Auth.KeycloakOIDC.Config.AllowedPrincipalIDs = configuredPrincipalIDs

		if err := k.client.Auth.KeycloakOIDC.Disable(); err != nil {
			return err
		}

		if _, err := authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked); err != nil {
			return err
		}

		if err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC); err != nil {
			return err
		}

		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)

		return err
	})

	logrus.Info("Disabling Keycloak OIDC so the enable path runs against a fresh provider")
	err = k.client.Auth.KeycloakOIDC.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak OIDC")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	k.client.Auth.KeycloakOIDC.Config.AccessMode = authactions.AccessModeRestricted
	k.client.Auth.KeycloakOIDC.Config.AllowedPrincipalIDs = nil

	logrus.Info("Enabling Keycloak OIDC by writing the auth config directly, into restricted access mode with an empty allow list")
	err = k.client.Auth.KeycloakOIDC.Enable()
	require.NoError(k.T(), err, "Writing the auth config runs no login for Rancher to check access against, so nothing stops the provider coming up restricted to an empty allow list")

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak OIDC config")
	require.True(k.T(), keycloakConfig.Enabled, "Keycloak OIDC should be enabled after an accepted config write")
	require.Equal(k.T(), authactions.AccessModeRestricted, keycloakConfig.AccessMode, "The provider should have come up in the access mode it was enabled with")
	require.NotContains(k.T(), keycloakConfig.AllowedPrincipalIDs, k.adminPrincipalID, "Writing the config adds no principal of its own, so the administrator who enabled the provider is named nowhere in the allow list that now governs it")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCEnableWithAdminLoginChecksAccessMode() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	configuredAccessMode := k.client.Auth.KeycloakOIDC.Config.AccessMode
	configuredPrincipalIDs := slices.Clone(k.client.Auth.KeycloakOIDC.Config.AllowedPrincipalIDs)
	subSession.RegisterCleanupFunc(func() error {
		k.client.Auth.KeycloakOIDC.Config.AccessMode = configuredAccessMode
		k.client.Auth.KeycloakOIDC.Config.AllowedPrincipalIDs = configuredPrincipalIDs

		if err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC); err != nil {
			return err
		}

		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)

		return err
	})

	logrus.Info("Disabling Keycloak OIDC so the enable path runs against a fresh provider")
	err = k.client.Auth.KeycloakOIDC.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak OIDC")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	k.client.Auth.KeycloakOIDC.Config.AccessMode = authactions.AccessModeRestricted
	k.client.Auth.KeycloakOIDC.Config.AllowedPrincipalIDs = nil

	logrus.Info("Enabling Keycloak OIDC through an administrator sign-in, into restricted access mode with an empty allow list")
	err = k.client.Auth.KeycloakOIDC.EnableWithAdminLogin(k.adminUser.Username, k.adminUser.Password)
	require.Error(k.T(), err, "Enabling through a sign-in resolves the administrator against the allow list before the config is saved, so an empty allow list must lock the administrator out rather than let them enable a provider they could not then use")

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak OIDC config")
	require.False(k.T(), keycloakConfig.Enabled, "Keycloak OIDC should remain disabled after a rejected sign-in")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCNestedGroupsInClaim() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	tiers := []struct {
		description string
		user        authactions.User
		group       string
		ancestors   []string
	}{
		{
			description: "nested one deep",
			user:        k.authConfig.NestedUsers[0],
			group:       k.authConfig.NestedGroup,
			ancestors:   []string{k.authConfig.Group},
		},
		{
			description: "nested two deep",
			user:        k.authConfig.DoubleNestedUsers[0],
			group:       k.authConfig.DoubleNestedGroup,
			ancestors:   []string{k.authConfig.Group, k.authConfig.NestedGroup},
		},
	}

	for _, tier := range tiers {
		logrus.Infof("Reading the groups Keycloak claims for [%v], a member of group [%v] %v", tier.user.Username, tier.group, tier.description)
		claimedGroups, err := authactions.KeycloakOIDCClaimValues(k.client, tier.user, authactions.KeycloakOIDCGroupsClaim)
		require.NoError(k.T(), err, "Failed to capture the token Keycloak issues for user [%v]", tier.user.Username)

		require.Contains(k.T(), claimedGroups, tier.group, "Keycloak should claim the group [%v] user [%v] belongs to directly, otherwise no binding of any kind can reach them", tier.group, tier.user.Username)

		for _, ancestor := range tier.ancestors {
			require.NotContains(k.T(), claimedGroups, ancestor, "The groups claim carries direct memberships alone, so ancestor group [%v] must not appear for user [%v]", ancestor, tier.user.Username)
		}
	}
}

func TestKeycloakOIDCAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(KeycloakOIDCAuthProviderSuite))
}

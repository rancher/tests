//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package keycloakoidc

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
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

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCAdminLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	authenticatedUser, err := authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(k.T(), err, "Failed to retrieve the user the OpenID Connect session authenticated as")
	require.Contains(k.T(), authenticatedUser.PrincipalIDs, k.adminPrincipalID, "Session should resolve to the Keycloak principal")

	var localPrincipals []string
	for _, principalID := range authenticatedUser.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.LocalPrincipalPrefix) {
			localPrincipals = append(localPrincipals, principalID)
		}
	}
	require.NotEmpty(k.T(), localPrincipals, "Enabling should have attached the Keycloak identity to the existing Rancher administrator, so the same user should still hold its local principal")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeUnrestricted, newAuthConfig.AccessMode, "Access mode should be unrestricted")

	allUsers := slices.Concat(k.authConfig.Users, k.authConfig.ExcludedUsers)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(k.T(), err, "Every Keycloak user should be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, authactions.AccessModeRestricted+" access mode", true)
	require.NoError(k.T(), err, "Members of the allowed group should be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRestrictedAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.ExcludedUsers, authactions.AccessModeRestricted+" access mode", false)
	require.NoError(k.T(), err, "Users outside the allowed group should NOT be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRequiredAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, authactions.AccessModeRequired+" access mode", true)
	require.NoError(k.T(), err, "Authorized users should be able to login")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRequiredAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.ExcludedUsers, authactions.AccessModeRequired+" access mode", false)
	require.NoError(k.T(), err, "Unauthorized users should NOT be able to login")
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

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	for _, userInfo := range k.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(k.client, groupPrincipalID, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to get group CRTB")
	require.NotNil(k.T(), foundCRTB, "Cluster role binding should exist for group")

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project and namespace")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	logrus.Infof("Granting Keycloak group [%v] the %v role on project [%v]", k.authConfig.Group, rbac.ProjectOwner, projectName)
	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, groupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(k.T(), err, "Failed to create PRTB")
	require.NotNil(k.T(), groupPRTBResp, "PRTB should be created")

	for _, userInfo := range k.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(k.T(), err, "User [%v] should be able to get project %s because the token places them in group [%v]", userInfo.Username, projectResp.Name, k.authConfig.Group)
	}

	err = authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(groupPRTBResp.Namespace, groupPRTBResp.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete PRTB: %s/%s", groupPRTBResp.Namespace, groupPRTBResp.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list users outside the allowed group to prove the group binding does not reach them")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range k.authConfig.ExcludedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(k.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(k.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	for _, userInfo := range k.authConfig.Users {
		userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, userInfo)
		userPRTB := &managementv3.ProjectRoleTemplateBinding{
			ObjectMeta: metav1.ObjectMeta{
				Namespace:    prtbNamespace,
				GenerateName: "prtb-",
			},
			ProjectName:       projectName,
			UserPrincipalName: userPrincipalID,
			RoleTemplateName:  rbac.ProjectOwner.String(),
		}

		userPRTBResp, err := authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(userPRTB)
		require.NoError(k.T(), err, "Failed to create PRTB for user [%v]", userInfo.Username)
		require.NotNil(k.T(), userPRTBResp, "PRTB should be created for user [%v]", userInfo.Username)
	}

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	allowedPrincipalIDs := []string{groupPrincipalID, k.adminPrincipalID}

	logrus.Infof("Restricting Keycloak OIDC to group [%v] and the administrator", k.authConfig.Group)
	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "restricted access mode holding cluster and project bindings", true)
	require.NoError(k.T(), err, "Members bound to the cluster and to the project should still be able to login once the provider is restricted to their group")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak OIDC auth input must list a user outside the allowed group, since this test admits one on a project binding alone")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(k.T(), err, "Failed to create group cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(k.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	outsider := k.authConfig.ExcludedUsers[0]
	outsiderPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, outsider)

	logrus.Infof("Signing user [%v] in while access is still unrestricted, so that a Rancher user record exists for them", outsider.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{outsider}, "unrestricted access mode", true)
	require.NoError(k.T(), err, "User [%v] should be able to login before the provider is restricted; restricted access mode reads a user's bindings off their Rancher user record, which only a login creates", outsider.Username)

	_, err = userapi.WaitForUserByPrincipalID(k.client, outsiderPrincipalID)
	require.NoError(k.T(), err, "Login should have created a Rancher user record carrying principal [%v]", outsiderPrincipalID)

	logrus.Infof("Granting user [%v], who is outside group [%v], the %v role on project [%v]", outsider.Username, k.authConfig.Group, rbac.ProjectOwner, projectName)
	outsiderPRTB := &managementv3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    prtbNamespace,
			GenerateName: "prtb-",
		},
		ProjectName:       projectName,
		UserPrincipalName: outsiderPrincipalID,
		RoleTemplateName:  rbac.ProjectOwner.String(),
	}
	outsiderPRTBResp, err := authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(outsiderPRTB)
	require.NoError(k.T(), err, "Failed to create PRTB for user [%v]", outsider.Username)
	require.NotNil(k.T(), outsiderPRTBResp, "PRTB should be created for user [%v]", outsider.Username)

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	allowedPrincipalIDs := []string{groupPrincipalID, k.adminPrincipalID}

	logrus.Infof("Restricting Keycloak OIDC to group [%v] and the administrator, leaving user [%v] listed nowhere", k.authConfig.Group, outsider.Username)
	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")
	require.ElementsMatch(k.T(), allowedPrincipalIDs, newAuthConfig.AllowedPrincipalIDs, "Allowed principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "restricted access mode as members of an allowed group", true)
	require.NoError(k.T(), err, "Members of the allowed group should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{outsider}, "restricted access mode as a project member only", true)
	require.NoError(k.T(), err, "Restricted access mode should admit user [%v] on their project binding alone, even though no principal of theirs is in the allow list", outsider.Username)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCRequiredModeRevokedPrincipalLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	grantedPrincipalIDs := []string{
		authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group),
		k.adminPrincipalID,
	}
	for _, user := range k.authConfig.Users {
		grantedPrincipalIDs = append(grantedPrincipalIDs, authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, user))
	}

	logrus.Infof("Granting Keycloak group [%v] and its members access in required mode", k.authConfig.Group)
	grantedConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, grantedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRequired, grantedConfig.AccessMode, "Access mode should be required")
	require.ElementsMatch(k.T(), grantedPrincipalIDs, grantedConfig.AllowedPrincipalIDs, "Granted principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "required access mode with principals granted", true)
	require.NoError(k.T(), err, "Granted users should be able to login")

	revokedPrincipalIDs := []string{k.adminPrincipalID}

	logrus.Infof("Revoking Keycloak group [%v] and its members, leaving only the administrator allowed", k.authConfig.Group)
	revokedConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakOIDC, authactions.AccessModeRequired, revokedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to revoke principals")
	require.Equal(k.T(), authactions.AccessModeRequired, revokedConfig.AccessMode, "Access mode should remain required")
	require.ElementsMatch(k.T(), revokedPrincipalIDs, revokedConfig.AllowedPrincipalIDs, "Revoked principals should no longer appear in the allow list")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, k.authConfig.Users, "required access mode after principals revoked", false)
	require.NoError(k.T(), err, "Revoked users should NOT be able to login")

	stillAllowed := []authactions.User{{Username: k.adminUser.Username, Password: k.adminUser.Password}}
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, stillAllowed, "required access mode after principals revoked", true)
	require.NoError(k.T(), err, "The administrator, whose principal was left in the allow list, should still be able to login")
}

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

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCPrincipalSearchFindsLocalUserByDisplayName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Creating a local user whose display name differs from its username")
	localUser, err := userapi.CreateUser(authAdmin)
	require.NoError(k.T(), err, "Failed to create local user")
	require.NotEqual(k.T(), localUser.Username, localUser.DisplayName, "Local user display name must differ from its username for this search to be meaningful")

	logrus.Infof("Searching principals for local user by username [%v]", localUser.Username)
	err = authactions.VerifyPrincipalIsLocal(k.client, localUser.Username)
	require.NoError(k.T(), err, "Local user [%v] should be findable by username while Keycloak OIDC is enabled", localUser.Username)

	logrus.Infof("Searching principals for local user by display name [%v]", localUser.DisplayName)
	err = authactions.VerifyPrincipalIsLocal(k.client, localUser.DisplayName)
	require.NoError(k.T(), err, "Local user [%v] should be findable by display name [%v]", localUser.Username, localUser.DisplayName)
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

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCLoginAttachesExternalPrincipal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak user %s so that the login flow attaches its external principal", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.Contains(k.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(k.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCDisableRemovesExternalPrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak user %s so that a user record carrying its external principal exists", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak user should be able to login")

	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.ElementsMatch(k.T(), []string{userPrincipalID, authactions.LocalPrincipalPrefix + attachedUser.Name}, attachedUser.PrincipalIDs,
		"User %s should carry only the Keycloak principal and the local principal added alongside it, so that disabling the provider leaves it with no external identity", attachedUser.Name)

	logrus.Info("Disabling Keycloak OIDC so that the cleanup service reconciles principals it emitted")
	err = k.client.Auth.KeycloakOIDC.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak OIDC")

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak OIDC so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakOIDC, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Disabling the provider should leave no user carrying a Keycloak principal")

	logrus.Infof("Verifying that user %s was removed rather than left behind holding only its local principal", attachedUser.Name)
	err = userapi.WaitForUserDeletion(k.client, attachedUser.Name)
	require.NoError(k.T(), err, "Disabling the provider should remove a user whose only external identity that provider emitted, leaving no record that could later be rebound")
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

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCParentGroupBindingDoesNotReachNestedMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	directMember := &v3.User{
		Username: k.authConfig.Users[0].Username,
		Password: k.authConfig.Users[0].Password,
	}
	directClient, err := authactions.LoginAsAuthUser(authAdmin, directMember, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to login user [%v]", directMember.Username)

	rbac.VerifyUserCanListCluster(k.T(), k.client, directClient, k.cluster.ID, rbac.ClusterOwner)

	nestedMembers := slices.Concat(k.authConfig.NestedUsers, k.authConfig.DoubleNestedUsers)

	for _, userInfo := range nestedMembers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(k.T(), err, "User [%v] sits beneath group [%v] rather than in it, and the groups claim names only their direct group, so the binding on [%v] must not reach them", user.Username, k.authConfig.Group, groupPrincipalID)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCNestedGroupBindingGrantsItsOwnMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	nestedPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.NestedGroup)

	logrus.Infof("Granting nested Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.NestedGroup, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, nestedPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "A nested group should be bindable by its own name, since Rancher takes group names off the token and never asks Keycloak where they sit")

	for _, userInfo := range k.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	for _, userInfo := range k.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakOIDC)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(k.T(), err, "User [%v] belongs to group [%v] beneath [%v], and a nested group is as flat to Rancher as any other, so the binding must not descend to them either", user.Username, k.authConfig.DoubleNestedGroup, k.authConfig.NestedGroup)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCFullGroupPathClaimGrantsAncestors() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Infof("Asking Keycloak to name each group by its full path in the %v claim", authactions.KeycloakOIDCFullGroupPathClaim)
	restoreGroupClaim, err := authactions.SetKeycloakOIDCGroupClaim(k.client, k.keycloak, k.clientID, authactions.KeycloakOIDCFullGroupPathClaim, true)
	subSession.RegisterCleanupFunc(restoreGroupClaim)
	require.NoError(k.T(), err, "Failed to switch the Keycloak groups mapper to full paths")

	nestedUser := k.authConfig.NestedUsers[0]

	claimedPaths, err := authactions.KeycloakOIDCClaimValues(k.client, nestedUser, authactions.KeycloakOIDCFullGroupPathClaim)
	require.NoError(k.T(), err, "Failed to capture the token Keycloak issues for user [%v]", nestedUser.Username)

	nestedPath := fmt.Sprintf("/%s/%s", k.authConfig.Group, k.authConfig.NestedGroup)
	require.Contains(k.T(), claimedPaths, nestedPath, "Keycloak should name group [%v] by the path [%v] once the mapper emits full paths", k.authConfig.NestedGroup, nestedPath)

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, k.authConfig.Group)

	logrus.Infof("Granting the parent Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	nestedMember := &v3.User{
		Username: nestedUser.Username,
		Password: nestedUser.Password,
	}
	nestedClient, err := authactions.LoginAsAuthUser(authAdmin, nestedMember, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to login user [%v]", nestedUser.Username)

	rbac.VerifyUserCanListCluster(k.T(), k.client, nestedClient, k.cluster.ID, rbac.ClusterOwner)

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func TestKeycloakOIDCAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(KeycloakOIDCAuthProviderSuite))
}

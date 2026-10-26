//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package keycloakoidc

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	secretapi "github.com/rancher/tests/actions/kubeapi/secrets"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/authproviders"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/framework"
	"github.com/rancher/tfp-automation/framework/cleanup"
	"github.com/rancher/tfp-automation/framework/set/resources/rancher2"
	"github.com/rancher/tfp-automation/tests/extensions/authprovider"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KeycloakOIDCTerraformSuite struct {
	suite.Suite
	session          *session.Session
	keycloakSession  *session.Session
	client           *rancher.Client
	keycloak         *keycloak.Client
	cluster          *v3.Cluster
	adminUser        *v3.User
	authConfig       *authactions.ExternalAuthConfig
	cattleConfig     map[string]any
	rancherConfig    *rancher.Config
	terraformConfig  *tfpConfig.TerraformConfig
	terratestConfig  *tfpConfig.TerratestConfig
	terraformOptions *terraform.Options
	keyPath          string
}

func (k *KeycloakOIDCTerraformSuite) SetupSuite() {
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

	k.adminUser = &v3.User{
		Username: keycloakOIDCFixture.Admin.Username,
		Password: keycloakOIDCFixture.Admin.Password,
	}

	logrus.Info("Loading terraform configuration from config file")
	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	require.NotNil(k.T(), k.cattleConfig, "Terraform configuration is not provided")

	k.rancherConfig, k.terraformConfig, k.terratestConfig, _ = tfpConfig.LoadTFPConfigs(k.cattleConfig)
	k.rancherConfig.AdminToken = k.client.RancherConfig.AdminToken

	if k.rancherConfig.Insecure == nil {
		k.rancherConfig.Insecure = k.client.RancherConfig.Insecure
	}

	require.NotNil(k.T(), k.rancherConfig.Insecure, "Insecure should be set in the rancher config")

	logrus.Info("Building the Keycloak OIDC terraform configuration from the realm the fixture registered")
	k.terraformConfig.AuthProvider = authproviders.KeycloakOIDC

	providerConfig := k.client.Auth.KeycloakOIDC.Config
	keycloakConfig := &k.terraformConfig.KeycloakOIDCConfig

	keycloakConfig.ClientID = providerConfig.ClientID
	keycloakConfig.ClientSecret = providerConfig.ClientSecret
	keycloakConfig.Issuer = providerConfig.Issuer
	keycloakConfig.RancherURL = providerConfig.RancherURL
	keycloakConfig.AuthEndpoint = providerConfig.AuthEndpoint
	keycloakConfig.TokenEndpoint = providerConfig.TokenEndpoint
	keycloakConfig.UserInfoEndpoint = providerConfig.UserInfoEndpoint
	keycloakConfig.JWKSUrl = providerConfig.JWKSUrl
	keycloakConfig.EndSessionEndpoint = providerConfig.EndSessionEndpoint
	keycloakConfig.Scopes = providerConfig.Scopes
	keycloakConfig.GroupsField = providerConfig.GroupsClaim
	keycloakConfig.GroupSearchEnabled = providerConfig.GroupSearchEnabled
	keycloakConfig.Certificate = providerConfig.Certificate
	keycloakConfig.PrivateKey = providerConfig.PrivateKey
	keycloakConfig.AccessMode = authactions.AccessModeUnrestricted

	require.NotEmpty(k.T(), keycloakConfig.ClientID, "Keycloak OIDC setup should have produced the client terraform registers")
	require.NotEmpty(k.T(), keycloakConfig.ClientSecret, "Keycloak OIDC setup should have produced the client secret terraform writes")
	require.NotEmpty(k.T(), keycloakConfig.Issuer, "Keycloak OIDC setup should have produced the issuer Rancher reads its endpoints from")

	logrus.Info("Setting up the terraform workspace for the Keycloak OIDC auth config resource")
	k.terratestConfig.PathToRepo = filepath.Join(k.terratestConfig.PathToRepo, authproviders.KeycloakOIDC)

	_, k.keyPath = rancher2.SetKeyPath(keypath.RancherKeyPath, k.terratestConfig.PathToRepo, "")
	require.NoError(k.T(), os.MkdirAll(k.keyPath, authactions.TerraformWorkspacePermissions), "Failed to create the terraform workspace directory "+k.keyPath)

	k.terraformOptions = framework.Setup(k.T(), k.terraformConfig, k.terratestConfig, k.keyPath)
}

func (k *KeycloakOIDCTerraformSuite) TearDownSuite() {
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

func (k *KeycloakOIDCTerraformSuite) TestKeycloakOIDCTerraformEnableProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(k.T(), k.terraformOptions, k.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak OIDC so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	logrus.Info("Enabling Keycloak OIDC through terraform")
	authprovider.Enable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak OIDC config")
	require.True(k.T(), keycloakConfig.Enabled, "Keycloak OIDC should be enabled after terraform apply")
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

	logrus.Info("Logging in as the Keycloak OIDC admin to confirm the terraform-enabled provider authenticates")
	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak OIDC user %s", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "provider enabled through terraform", true)
	require.NoError(k.T(), err, "Keycloak OIDC user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.Contains(k.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(k.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (k *KeycloakOIDCTerraformSuite) TestKeycloakOIDCTerraformDisableProvider() {
	k.T().Skip("Known issue: https://github.com/rancher/terraform-provider-rancher2/issues/2512")

	subSession := k.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(k.T(), k.terraformOptions, k.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak OIDC so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	logrus.Info("Enabling Keycloak OIDC through terraform")
	authprovider.Enable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak OIDC user %s so that a user record carrying its external principal exists", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak OIDC user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope an admin client to the test subsession")

	logrus.Info("Binding the Keycloak OIDC group to a cluster role so that disabling the provider has a binding to clean up")
	_, err = authactions.SetupExternalRequiredAccessModePrincipals(subClient, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to bind the Keycloak OIDC group to a cluster role")

	logrus.Info("Disabling Keycloak OIDC through terraform apply with enabled=false")
	authprovider.Disable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	err = authactions.VerifyProviderDisabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Keycloak OIDC should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through Keycloak OIDC no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(k.T(), err, "Sessions established through Keycloak OIDC should be rejected once it is disabled")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Disabling the provider should leave no user carrying a Keycloak OIDC principal")

	err = rbacapi.VerifyBindingsDeletedForProvider(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Disabling the provider should remove bindings that reference its principals")
}

func (k *KeycloakOIDCTerraformSuite) TestKeycloakOIDCTerraformDestroyProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(k.T(), k.terraformOptions, k.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak OIDC so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	})

	logrus.Info("Enabling Keycloak OIDC through terraform")
	authprovider.Enable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.KeycloakOIDC, keycloakUser)

	logrus.Infof("Logging in as Keycloak OIDC user %s so that a user record carrying its external principal exists", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakOIDC, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak OIDC user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope an admin client to the test subsession")

	logrus.Info("Binding the Keycloak OIDC group to a cluster role so that destroying the provider has a binding to clean up")
	_, err = authactions.SetupExternalRequiredAccessModePrincipals(subClient, k.cluster.ID, k.authConfig, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to bind the Keycloak OIDC group to a cluster role")

	logrus.Info("Destroying the Keycloak OIDC auth config resource through terraform")
	authprovider.Destroy(k.T(), k.terraformOptions)

	err = authactions.VerifyProviderDisabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Keycloak OIDC should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through Keycloak OIDC no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(k.T(), err, "Sessions established through Keycloak OIDC should be rejected once it is destroyed")

	globalDataNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: rbac.GlobalDataNS}}

	err = secretapi.WaitForSecretInNamespaces(k.client, extclusterapi.LocalCluster, authactions.KeycloakOIDCClientSecretID, []*corev1.Namespace{globalDataNS}, false)
	require.NoError(k.T(), err, "The client secret secret should be deleted")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Destroying the provider should leave no user carrying a Keycloak OIDC principal")

	err = rbacapi.VerifyBindingsDeletedForProvider(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Destroying the provider should remove bindings that reference its principals")
}

func TestKeycloakOIDCTerraformSuite(t *testing.T) {
	suite.Run(t, new(KeycloakOIDCTerraformSuite))
}

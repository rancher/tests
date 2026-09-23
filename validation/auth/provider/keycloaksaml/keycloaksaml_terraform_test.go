//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package keycloaksaml

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

type KeycloakSAMLTerraformSuite struct {
	suite.Suite
	session          *session.Session
	keycloakSession  *session.Session
	client           *rancher.Client
	keycloak         *keycloak.Client
	cluster          *v3.Cluster
	adminUser        *v3.User
	authConfig       *authactions.SAMLAuthConfig
	cattleConfig     map[string]any
	rancherConfig    *rancher.Config
	terraformConfig  *tfpConfig.TerraformConfig
	terratestConfig  *tfpConfig.TerratestConfig
	terraformOptions *terraform.Options
	keyPath          string
}

func (k *KeycloakSAMLTerraformSuite) SetupSuite() {
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
	k.keycloak, err = authactions.NewKeycloakClient(k.keycloakSession)
	require.NoError(k.T(), err, "Failed to create Keycloak admin client")

	logrus.Info("Registering the Rancher SAML client and settling the test accounts in the Keycloak realm")
	keycloakSAMLFixture, err := authactions.SetupKeycloakSAML(k.client, k.keycloak)
	require.NoError(k.T(), err, "Failed to set up the Keycloak realm for Keycloak SAML")

	k.authConfig = keycloakSAMLFixture.AuthInput
	require.NotEmpty(k.T(), k.authConfig.Group, "Keycloak SAML setup should have settled on the allowed group")
	require.NotEmpty(k.T(), k.authConfig.Users, "Keycloak SAML setup should have settled on the users to sign in as")

	k.adminUser = &v3.User{
		Username: keycloakSAMLFixture.Admin.Username,
		Password: keycloakSAMLFixture.Admin.Password,
	}

	logrus.Info("Settling the service provider signing pair so that terraform and the SAML logins share one certificate")
	err = authactions.EnsureKeycloakSAMLKeyPair(k.client)
	require.NoError(k.T(), err, "Failed to settle the Keycloak SAML service provider signing pair")

	logrus.Info("Loading terraform configuration from config file")
	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	require.NotNil(k.T(), k.cattleConfig, "Terraform configuration is not provided")

	k.rancherConfig, k.terraformConfig, k.terratestConfig, _ = tfpConfig.LoadTFPConfigs(k.cattleConfig)
	k.rancherConfig.AdminToken = k.client.RancherConfig.AdminToken

	if k.rancherConfig.Insecure == nil {
		k.rancherConfig.Insecure = k.client.RancherConfig.Insecure
	}

	require.NotNil(k.T(), k.rancherConfig.Insecure, "Insecure should be set in the rancher config")

	logrus.Info("Building the Keycloak SAML terraform configuration from the realm the fixture registered")
	k.terraformConfig.AuthProvider = authproviders.KeycloakSAML

	providerConfig := k.client.Auth.KeycloakSAML.Config
	keycloakConfig := &k.terraformConfig.KeycloakSAMLConfig

	keycloakConfig.DisplayNameField = providerConfig.DisplayNameField
	keycloakConfig.GroupsField = providerConfig.GroupsField
	keycloakConfig.UIDField = providerConfig.UIDField
	keycloakConfig.UserNameField = providerConfig.UserNameField
	keycloakConfig.IdpMetadataContent = providerConfig.IDPMetadataContent
	keycloakConfig.RancherAPIHost = providerConfig.RancherAPIHost
	keycloakConfig.EntityID = providerConfig.EntityID
	keycloakConfig.SPCert = providerConfig.SpCert
	keycloakConfig.SPKey = providerConfig.SpKey
	keycloakConfig.AccessMode = authactions.AccessModeUnrestricted

	require.NotEmpty(k.T(), keycloakConfig.IdpMetadataContent, "Keycloak SAML setup should have produced the identity provider metadata terraform writes")
	require.NotEmpty(k.T(), keycloakConfig.RancherAPIHost, "Keycloak SAML setup should have produced the host Keycloak issues assertions for")

	logrus.Info("Setting up the terraform workspace for the Keycloak SAML auth config resource")
	k.terratestConfig.PathToRepo = filepath.Join(k.terratestConfig.PathToRepo, authproviders.KeycloakSAML)

	_, k.keyPath = rancher2.SetKeyPath(keypath.RancherKeyPath, k.terratestConfig.PathToRepo, "")
	require.NoError(k.T(), os.MkdirAll(k.keyPath, authactions.TerraformWorkspacePermissions), "Failed to create the terraform workspace directory "+k.keyPath)

	k.terraformOptions = framework.Setup(k.T(), k.terraformConfig, k.terratestConfig, k.keyPath)
}

func (k *KeycloakSAMLTerraformSuite) TearDownSuite() {
	defer k.keycloakSession.Cleanup()
	defer k.session.Cleanup()

	if k.client != nil {
		keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
		if err == nil && keycloakConfig.Enabled {
			logrus.Info("Disabling Keycloak SAML authentication after test suite")
			err := k.client.Auth.KeycloakSAML.Disable()
			require.NoError(k.T(), err, "Failed to disable Keycloak SAML in teardown")
		}
	}
}

func (k *KeycloakSAMLTerraformSuite) TestKeycloakSAMLTerraformEnableProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(k.T(), k.terraformOptions, k.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak SAML so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	})

	logrus.Info("Enabling Keycloak SAML through terraform")
	authprovider.Enable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak SAML config")
	require.True(k.T(), keycloakConfig.Enabled, "Keycloak SAML should be enabled after terraform apply")
	require.Equal(k.T(), v3.KeyCloakConfigType, keycloakConfig.Type, "Auth config should be stored as the Keycloak SAML subtype")
	require.Equal(k.T(), authactions.AuthProvCleanupAnnotationValUnlocked, keycloakConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := k.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.KeycloakSAMLKeySecretID,
		metav1.GetOptions{},
	)
	require.NoError(k.T(), err, "Rancher should move the service provider signing key out of the auth config into a secret")
	require.NotEmpty(k.T(), secret.Data, "Signing key secret should hold the key")

	logrus.Info("Logging in as the Keycloak SAML admin to confirm the terraform-enabled provider authenticates")
	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, keycloakUser)

	logrus.Infof("Logging in as Keycloak SAML user %s", authactions.PrincipalNameOf(keycloakUser))
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{keycloakUser}, "provider enabled through terraform", true)
	require.NoError(k.T(), err, "Keycloak SAML user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.Contains(k.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(k.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (k *KeycloakSAMLTerraformSuite) TestKeycloakSAMLTerraformDisableProvider() {
	k.T().Skip("Known issue: https://github.com/rancher/terraform-provider-rancher2/issues/2512")

	subSession := k.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(k.T(), k.terraformOptions, k.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak SAML so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	})

	logrus.Info("Enabling Keycloak SAML through terraform")
	authprovider.Enable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, keycloakUser)

	logrus.Infof("Logging in as Keycloak SAML user %s so that a user record carrying its external principal exists", authactions.PrincipalNameOf(keycloakUser))
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak SAML user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope an admin client to the test subsession")

	logrus.Info("Binding the Keycloak SAML group to a cluster role so that disabling the provider has a binding to clean up")
	_, err = authactions.SetupSAMLRequiredAccessModePrincipals(subClient, k.cluster.ID, k.authConfig, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to bind the Keycloak SAML group to a cluster role")

	logrus.Info("Disabling Keycloak SAML through terraform apply with enabled=false")
	authprovider.Disable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	err = authactions.VerifyProviderDisabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Keycloak SAML should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through Keycloak SAML no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(k.T(), err, "Sessions established through Keycloak SAML should be rejected once it is disabled")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Disabling the provider should leave no user carrying a Keycloak SAML principal")

	err = rbacapi.VerifyBindingsDeletedForProvider(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Disabling the provider should remove bindings that reference its principals")
}

func (k *KeycloakSAMLTerraformSuite) TestKeycloakSAMLTerraformDestroyProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(k.T(), k.terraformOptions, k.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak SAML so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	})

	logrus.Info("Enabling Keycloak SAML through terraform")
	authprovider.Enable(k.T(), k.rancherConfig, k.terraformConfig, k.terratestConfig, k.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, keycloakUser)

	logrus.Infof("Logging in as Keycloak SAML user %s so that a user record carrying its external principal exists", authactions.PrincipalNameOf(keycloakUser))
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak SAML user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope an admin client to the test subsession")

	logrus.Info("Binding the Keycloak SAML group to a cluster role so that destroying the provider has a binding to clean up")
	_, err = authactions.SetupSAMLRequiredAccessModePrincipals(subClient, k.cluster.ID, k.authConfig, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to bind the Keycloak SAML group to a cluster role")

	logrus.Info("Destroying the Keycloak SAML auth config resource through terraform")
	authprovider.Destroy(k.T(), k.terraformOptions)

	err = authactions.VerifyProviderDisabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Keycloak SAML should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through Keycloak SAML no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(k.T(), err, "Sessions established through Keycloak SAML should be rejected once it is destroyed")

	globalDataNS := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: rbac.GlobalDataNS}}

	err = secretapi.WaitForSecretInNamespaces(k.client, extclusterapi.LocalCluster, authactions.KeycloakSAMLKeySecretID, []*corev1.Namespace{globalDataNS}, false)
	require.NoError(k.T(), err, "The service provider signing key secret should be deleted")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Destroying the provider should leave no user carrying a Keycloak SAML principal")

	err = rbacapi.VerifyBindingsDeletedForProvider(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Destroying the provider should remove bindings that reference its principals")
}

func TestKeycloakSAMLTerraformSuite(t *testing.T) {
	suite.Run(t, new(KeycloakSAMLTerraformSuite))
}

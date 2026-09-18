//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package activedirectory

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	authactions "github.com/rancher/tests/actions/auth"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	activeDirectoryPort  = 389
	workspacePermissions = 0o700
)

type ActiveDirectoryTerraformSuite struct {
	activeDirectorySuite
	cattleConfig     map[string]any
	rancherConfig    *rancher.Config
	terraformConfig  *tfpConfig.TerraformConfig
	terratestConfig  *tfpConfig.TerratestConfig
	terraformOptions *terraform.Options
	keyPath          string
}

func (a *ActiveDirectoryTerraformSuite) SetupSuite() {
	a.activeDirectorySuite.SetupSuite()

	logrus.Info("Loading terraform configuration from config file")
	a.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	require.NotNil(a.T(), a.cattleConfig, "Terraform configuration is not provided")

	a.rancherConfig, a.terraformConfig, a.terratestConfig, _ = tfpConfig.LoadTFPConfigs(a.cattleConfig)
	a.rancherConfig.AdminToken = a.client.RancherConfig.AdminToken

	if a.rancherConfig.Insecure == nil {
		a.rancherConfig.Insecure = a.client.RancherConfig.Insecure
	}

	require.NotNil(a.T(), a.rancherConfig.Insecure, "Insecure should be set in the rancher config")

	logrus.Info("Building the Active Directory terraform configuration from the activeDirectory config file block")
	a.terraformConfig.AuthProvider = authproviders.AD

	activeDirectory := a.client.Auth.ActiveDirectory.Config
	adConfig := &a.terraformConfig.ADConfig

	adConfig.Servers = []string{directoryServer(activeDirectory.Hostname, activeDirectory.IP)}
	adConfig.Port = int64(activeDirectory.Port)

	if adConfig.Port == 0 {
		adConfig.Port = activeDirectoryPort
	}

	adConfig.TLS = &activeDirectory.TLS
	adConfig.StartTLS = &activeDirectory.StartTLS
	adConfig.ServiceAccountUsername = activeDirectory.ServiceAccount.DistinguishedName
	adConfig.ServiceAccountPassword = activeDirectory.ServiceAccount.Password
	adConfig.UserSearchBase = activeDirectory.Users.SearchBase
	adConfig.GroupSearchBase = activeDirectory.Groups.SearchBase
	adConfig.NestedGroupMembershipEnabled = &activeDirectory.Groups.NestedGroupMembershipEnabled
	adConfig.TestUsername = activeDirectory.Users.Admin.Username
	adConfig.TestPassword = activeDirectory.Users.Admin.Password

	logrus.Info("Setting up the terraform workspace for the Active Directory auth config resource")
	a.terratestConfig.PathToRepo = filepath.Join(a.terratestConfig.PathToRepo, authproviders.AD)

	_, a.keyPath = rancher2.SetKeyPath(keypath.RancherKeyPath, a.terratestConfig.PathToRepo, "")
	require.NoError(a.T(), os.MkdirAll(a.keyPath, workspacePermissions), "Failed to create the terraform workspace directory "+a.keyPath)

	a.terraformOptions = framework.Setup(a.T(), a.terraformConfig, a.terratestConfig, a.keyPath)
}

func directoryServer(hostname, ip string) string {
	if hostname != "" {
		return hostname
	}

	return ip
}

func (a *ActiveDirectoryTerraformSuite) TestActiveDirectoryTerraformEnableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(a.T(), a.terraformOptions, a.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Active Directory so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	logrus.Info("Enabling Active Directory through terraform")
	authprovider.Enable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	adConfig, err := a.client.Management.AuthConfig.ByID(authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to retrieve Active Directory config")
	require.True(a.T(), adConfig.Enabled, "Active Directory should be enabled after terraform apply")
	require.Equal(a.T(), authactions.AuthProvCleanupAnnotationValUnlocked, adConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := a.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.ActiveDirectoryPasswordSecretID,
		metav1.GetOptions{},
	)
	require.NoError(a.T(), err, "Failed to retrieve password secret")
	require.Equal(a.T(), a.client.Auth.ActiveDirectory.Config.ServiceAccount.Password, string(secret.Data["serviceaccountpassword"]), "Password mismatch")

	logrus.Info("Logging in as the Active Directory admin to confirm the terraform-enabled provider authenticates")
	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	adUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.ActiveDirectory, adUser.Username, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	groupPrincipalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, a.authConfig.Group, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	logrus.Infof("Verifying user search returns %s", userPrincipalID)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, adUser.Username, authactions.PrincipalTypeUser, userPrincipalID)
	require.NoError(a.T(), err, "User search should return the Active Directory user principal")

	logrus.Infof("Verifying group search returns %s", groupPrincipalID)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, a.authConfig.Group, authactions.PrincipalTypeGroup, groupPrincipalID)
	require.NoError(a.T(), err, "Group search should return the Active Directory group principal")

	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.ActiveDirectory, authactions.PrincipalTypeUser)
	require.NoError(a.T(), err, "The user principal should resolve to the Active Directory provider")

	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.ActiveDirectory, authactions.PrincipalTypeGroup)
	require.NoError(a.T(), err, "The group principal should resolve to the Active Directory provider")

	logrus.Infof("Logging in as Active Directory user %s", adUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, []authactions.User{adUser}, "provider enabled through terraform", true)
	require.NoError(a.T(), err, "Active Directory user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")
	require.Contains(a.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(a.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Failed to refresh group membership")

	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, a.authConfig.Group, authactions.PrincipalTypeGroup, groupPrincipalID)
	require.NoError(a.T(), err, "Group search should still return the Active Directory group after a refresh")
}

func (a *ActiveDirectoryTerraformSuite) TestActiveDirectoryTerraformDisableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(a.T(), a.terraformOptions, a.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Active Directory so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	logrus.Info("Enabling Active Directory through terraform")
	authprovider.Enable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	adUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.ActiveDirectory, adUser.Username, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	logrus.Infof("Logging in as Active Directory user %s so that a user record carrying its external principal exists", adUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, []authactions.User{adUser}, "external principal attachment", true)
	require.NoError(a.T(), err, "Active Directory user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")

	logrus.Info("Binding the Active Directory group to a cluster role so that disabling the provider has a binding to clean up")
	_, err = authactions.SetupRequiredAccessModePrincipals(authAdmin, a.cluster.ID, a.authConfig, authactions.ActiveDirectory, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	require.NoError(a.T(), err, "Failed to bind the Active Directory group to a cluster role")

	logrus.Info("Disabling Active Directory through terraform apply with enabled=false")
	authprovider.Disable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	err = authactions.VerifyProviderDisabled(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Active Directory should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through Active Directory no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(a.T(), err, "Sessions established through Active Directory should be rejected once it is disabled")

	logrus.Warn("Skipping the service account password secret check until rancher/terraform-provider-rancher2#2501 is fixed: disabling through terraform re-creates the secret Rancher deleted, and the destroy test still covers it")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Disabling the provider should leave no user carrying an Active Directory principal")

	err = authactions.VerifyBindingsDeleted(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Disabling the provider should remove bindings that reference its principals")
}

func (a *ActiveDirectoryTerraformSuite) TestActiveDirectoryTerraformDestroyProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(a.T(), a.terraformOptions, a.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Active Directory so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.ActiveDirectory)
	})

	logrus.Info("Enabling Active Directory through terraform")
	authprovider.Enable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	adUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.ActiveDirectory, adUser.Username, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	logrus.Infof("Logging in as Active Directory user %s so that a user record carrying its external principal exists", adUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.ActiveDirectory, []authactions.User{adUser}, "external principal attachment", true)
	require.NoError(a.T(), err, "Active Directory user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")

	logrus.Info("Binding the Active Directory group to a cluster role so that destroying the provider has a binding to clean up")
	_, err = authactions.SetupRequiredAccessModePrincipals(authAdmin, a.cluster.ID, a.authConfig, authactions.ActiveDirectory, a.client.Auth.ActiveDirectory.Config.Users.SearchBase, a.client.Auth.ActiveDirectory.Config.Groups.SearchBase)
	require.NoError(a.T(), err, "Failed to bind the Active Directory group to a cluster role")

	logrus.Info("Destroying the Active Directory auth config resource through terraform")
	authprovider.Destroy(a.T(), a.terraformOptions)

	err = authactions.VerifyProviderDisabled(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Active Directory should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through Active Directory no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(a.T(), err, "Sessions established through Active Directory should be rejected once it is destroyed")

	err = authactions.VerifySecretDeleted(a.client, authactions.ActiveDirectoryPasswordSecretID)
	require.NoError(a.T(), err, "The service account password secret should be deleted")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Destroying the provider should leave no user carrying an Active Directory principal")

	err = authactions.VerifyBindingsDeleted(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Destroying the provider should remove bindings that reference its principals")
}

func TestActiveDirectoryTerraformSuite(t *testing.T) {
	suite.Run(t, new(ActiveDirectoryTerraformSuite))
}

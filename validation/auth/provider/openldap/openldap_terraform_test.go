//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package openldap

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
	openLDAPPort         = 389
	workspacePermissions = 0o700
)

type OpenLDAPTerraformSuite struct {
	openLDAPSuite
	cattleConfig     map[string]any
	rancherConfig    *rancher.Config
	terraformConfig  *tfpConfig.TerraformConfig
	terratestConfig  *tfpConfig.TerratestConfig
	terraformOptions *terraform.Options
	keyPath          string
}

func (a *OpenLDAPTerraformSuite) SetupSuite() {
	a.openLDAPSuite.SetupSuite()

	logrus.Info("Loading terraform configuration from config file")
	a.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	require.NotNil(a.T(), a.cattleConfig, "Terraform configuration is not provided")

	a.rancherConfig, a.terraformConfig, a.terratestConfig, _ = tfpConfig.LoadTFPConfigs(a.cattleConfig)
	a.rancherConfig.AdminToken = a.client.RancherConfig.AdminToken

	if a.rancherConfig.Insecure == nil {
		a.rancherConfig.Insecure = a.client.RancherConfig.Insecure
	}

	require.NotNil(a.T(), a.rancherConfig.Insecure, "Insecure should be set in the rancher config")

	logrus.Info("Building the OpenLDAP terraform configuration from the openLDAP config file block")
	a.terraformConfig.AuthProvider = authproviders.OpenLDAP

	openLDAP := a.client.Auth.OLDAP.Config
	openLDAPConfig := &a.terraformConfig.OpenLDAPConfig

	openLDAPConfig.Servers = []string{directoryServer(openLDAP.Hostname, openLDAP.IP)}
	if openLDAPConfig.Port == 0 {
		openLDAPConfig.Port = openLDAPPort
	}

	openLDAPConfig.ServiceAccountDistinguisedName = openLDAP.ServiceAccount.DistinguishedName
	openLDAPConfig.ServiceAccountPassword = openLDAP.ServiceAccount.Password
	openLDAPConfig.UserSearchBase = openLDAP.Users.SearchBase
	openLDAPConfig.GroupSearchBase = openLDAP.Groups.SearchBase
	openLDAPConfig.NestedGroupMembershipEnabled = &openLDAP.Groups.NestedGroupMembershipEnabled
	openLDAPConfig.TestUsername = openLDAP.Users.Admin.Username
	openLDAPConfig.TestPassword = openLDAP.Users.Admin.Password

	logrus.Info("Setting up the terraform workspace for the OpenLDAP auth config resource")
	a.terratestConfig.PathToRepo = filepath.Join(a.terratestConfig.PathToRepo, authproviders.OpenLDAP)

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

func (a *OpenLDAPTerraformSuite) TestOpenLDAPTerraformEnableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(a.T(), a.terraformOptions, a.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling OpenLDAP so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.OpenLdap)
	})

	logrus.Info("Enabling OpenLDAP through terraform")
	authprovider.Enable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	ldapConfig, err := a.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to retrieve OpenLDAP config")
	require.True(a.T(), ldapConfig.Enabled, "OpenLDAP should be enabled after terraform apply")
	require.Equal(a.T(), authactions.AuthProvCleanupAnnotationValUnlocked, ldapConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := a.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.OpenLdapPasswordSecretID,
		metav1.GetOptions{},
	)
	require.NoError(a.T(), err, "Failed to retrieve password secret")
	require.Equal(a.T(), a.client.Auth.OLDAP.Config.ServiceAccount.Password, string(secret.Data["serviceaccountpassword"]), "Password mismatch")

	logrus.Info("Logging in as the OpenLDAP admin to confirm the terraform-enabled provider authenticates")
	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	ldapUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.OpenLdap, ldapUser.Username, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
	groupPrincipalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, a.authConfig.Group, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)

	logrus.Infof("Verifying user search returns %s", userPrincipalID)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, ldapUser.Username, authactions.PrincipalTypeUser, userPrincipalID)
	require.NoError(a.T(), err, "User search should return the OpenLDAP user principal")

	logrus.Infof("Verifying group search returns %s", groupPrincipalID)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, a.authConfig.Group, authactions.PrincipalTypeGroup, groupPrincipalID)
	require.NoError(a.T(), err, "Group search should return the OpenLDAP group principal")

	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.OpenLdap, authactions.PrincipalTypeUser)
	require.NoError(a.T(), err, "The user principal should resolve to the OpenLDAP provider")

	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.OpenLdap, authactions.PrincipalTypeGroup)
	require.NoError(a.T(), err, "The group principal should resolve to the OpenLDAP provider")

	logrus.Infof("Logging in as OpenLDAP user %s", ldapUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, []authactions.User{ldapUser}, "provider enabled through terraform", true)
	require.NoError(a.T(), err, "OpenLDAP user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")
	require.Contains(a.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(a.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")

	err = users.RefreshGroupMembership(authAdmin)
	require.NoError(a.T(), err, "Failed to refresh group membership")

	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, a.authConfig.Group, authactions.PrincipalTypeGroup, groupPrincipalID)
	require.NoError(a.T(), err, "Group search should still return the OpenLDAP group after a refresh")
}

func (a *OpenLDAPTerraformSuite) TestOpenLDAPTerraformDisableProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(a.T(), a.terraformOptions, a.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling OpenLDAP so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.OpenLdap)
	})

	logrus.Info("Enabling OpenLDAP through terraform")
	authprovider.Enable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	ldapUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.OpenLdap, ldapUser.Username, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)

	logrus.Infof("Logging in as OpenLDAP user %s so that a user record carrying its external principal exists", ldapUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, []authactions.User{ldapUser}, "external principal attachment", true)
	require.NoError(a.T(), err, "OpenLDAP user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")

	logrus.Info("Binding the OpenLDAP group to a cluster role so that disabling the provider has a binding to clean up")
	_, err = authactions.SetupRequiredAccessModePrincipals(authAdmin, a.cluster.ID, a.authConfig, authactions.OpenLdap, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
	require.NoError(a.T(), err, "Failed to bind the OpenLDAP group to a cluster role")

	logrus.Info("Disabling OpenLDAP through terraform apply with enabled=false")
	authprovider.Disable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	err = authactions.VerifyProviderDisabled(a.client, authactions.OpenLdap)
	require.NoError(a.T(), err, "OpenLDAP should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through OpenLDAP no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(a.T(), err, "Sessions established through OpenLDAP should be rejected once it is disabled")

	logrus.Warn("Skipping the service account password secret check until rancher/terraform-provider-rancher2#2501 is fixed: disabling through terraform re-creates the secret Rancher deleted, and the destroy test still covers it")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Disabling the provider should leave no user carrying an OpenLDAP principal")

	err = authactions.VerifyBindingsDeleted(a.client, authactions.OpenLdap)
	require.NoError(a.T(), err, "Disabling the provider should remove bindings that reference its principals")
}

func (a *OpenLDAPTerraformSuite) TestOpenLDAPTerraformDestroyProvider() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()
	defer cleanup.Cleanup(a.T(), a.terraformOptions, a.keyPath)

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling OpenLDAP so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(a.client, authactions.OpenLdap)
	})

	logrus.Info("Enabling OpenLDAP through terraform")
	authprovider.Enable(a.T(), a.rancherConfig, a.terraformConfig, a.terratestConfig, a.terraformOptions)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.OpenLdap)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	ldapUser := a.authConfig.Users[0]
	userPrincipalID := authactions.GetUserPrincipalID(authactions.OpenLdap, ldapUser.Username, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)

	logrus.Infof("Logging in as OpenLDAP user %s so that a user record carrying its external principal exists", ldapUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.OpenLdap, []authactions.User{ldapUser}, "external principal attachment", true)
	require.NoError(a.T(), err, "OpenLDAP user should be able to login")

	_, err = userapi.WaitForUserByPrincipalID(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Login should attach the external principal to a user record")

	logrus.Info("Binding the OpenLDAP group to a cluster role so that destroying the provider has a binding to clean up")
	_, err = authactions.SetupRequiredAccessModePrincipals(authAdmin, a.cluster.ID, a.authConfig, authactions.OpenLdap, a.client.Auth.OLDAP.Config.Users.SearchBase, a.client.Auth.OLDAP.Config.Groups.SearchBase)
	require.NoError(a.T(), err, "Failed to bind the OpenLDAP group to a cluster role")

	logrus.Info("Destroying the OpenLDAP auth config resource through terraform")
	authprovider.Destroy(a.T(), a.terraformOptions)

	err = authactions.VerifyProviderDisabled(a.client, authactions.OpenLdap)
	require.NoError(a.T(), err, "OpenLDAP should be disabled with its cleanup annotation locked")

	logrus.Info("Verifying that a session established through OpenLDAP no longer reaches the Rancher API")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(a.T(), err, "Sessions established through OpenLDAP should be rejected once it is destroyed")

	err = authactions.VerifySecretDeleted(a.client, authactions.OpenLdapPasswordSecretID)
	require.NoError(a.T(), err, "The service account password secret should be deleted")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(a.client, userPrincipalID)
	require.NoError(a.T(), err, "Destroying the provider should leave no user carrying an OpenLDAP principal")

	err = authactions.VerifyBindingsDeleted(a.client, authactions.OpenLdap)
	require.NoError(a.T(), err, "Destroying the provider should remove bindings that reference its principals")
}

func TestOpenLDAPTerraformSuite(t *testing.T) {
	suite.Run(t, new(OpenLDAPTerraformSuite))
}

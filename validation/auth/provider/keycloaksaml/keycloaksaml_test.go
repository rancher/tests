//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package keycloaksaml

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KeycloakSAMLAuthProviderSuite struct {
	suite.Suite
	session          *session.Session
	keycloakSession  *session.Session
	client           *rancher.Client
	keycloak         *keycloak.Client
	cluster          *v3.Cluster
	adminUser        *v3.User
	adminPrincipalID string
	entityID         string
	authConfig       *authactions.SAMLAuthConfig
}

func (k *KeycloakSAMLAuthProviderSuite) SetupSuite() {
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
	fixture, err := authactions.SetupKeycloakSAML(k.client, k.keycloak)
	require.NoError(k.T(), err, "Failed to set up the Keycloak realm for Keycloak SAML")

	k.authConfig = fixture.AuthInput
	require.NotEmpty(k.T(), k.authConfig.Group, "Keycloak SAML setup should have settled on the allowed group")
	require.NotEmpty(k.T(), k.authConfig.Users, "Keycloak SAML setup should have settled on the users to sign in as")
	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak SAML setup should have settled on a user outside the allowed group")
	require.NotEmpty(k.T(), k.authConfig.NestedGroup, "Keycloak SAML setup should have settled on a group nested beneath the allowed group")
	require.NotEmpty(k.T(), k.authConfig.NestedUsers, "Keycloak SAML setup should have settled on a member of the nested group")
	require.NotEmpty(k.T(), k.authConfig.DoubleNestedGroup, "Keycloak SAML setup should have settled on a group nested two deep")
	require.NotEmpty(k.T(), k.authConfig.DoubleNestedUsers, "Keycloak SAML setup should have settled on a member of the doubly nested group")

	k.adminUser = &v3.User{
		Username: fixture.Admin.Username,
		Password: fixture.Admin.Password,
	}
	k.adminPrincipalID = fixture.AdminPrincipalID
	k.entityID = fixture.EntityID
}

func (k *KeycloakSAMLAuthProviderSuite) TearDownSuite() {
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

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLEnableProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to enable Keycloak SAML")

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak SAML config")

	require.True(k.T(), keycloakConfig.Enabled, "Keycloak SAML should be enabled")
	require.Equal(k.T(), v3.KeyCloakConfigType, keycloakConfig.Type, "Auth config should be stored as the Keycloak SAML subtype")
	require.Equal(k.T(), authactions.AuthProvCleanupAnnotationValUnlocked, keycloakConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := k.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.KeycloakSAMLKeySecretID,
		metav1.GetOptions{},
	)
	require.NoError(k.T(), err, "Rancher should move the service provider signing key out of the auth config into a secret")
	require.NotEmpty(k.T(), secret.Data, "Signing key secret should hold the key")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLDisableAndReenableProvider() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to enable Keycloak SAML")

	err = k.client.Auth.KeycloakSAML.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak SAML")

	keycloakConfig, err := authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	require.False(k.T(), keycloakConfig.Enabled, "Keycloak SAML should be disabled")
	require.Equal(k.T(), authactions.AuthProvCleanupAnnotationValLocked, keycloakConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be locked")

	_, err = k.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.KeycloakSAMLKeySecretID,
		metav1.GetOptions{},
	)
	require.Error(k.T(), err, "Signing key secret should be removed when the provider is disabled")
	require.Contains(k.T(), err.Error(), "not found", "Should return not found error")

	err = authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to re-enable Keycloak SAML")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLAdminLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	authenticatedUser, err := authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(k.T(), err, "Failed to retrieve the user the SAML session authenticated as")
	require.Contains(k.T(), authenticatedUser.PrincipalIDs, k.adminPrincipalID, "Session should resolve to the Keycloak principal")

	var localPrincipals []string
	for _, principalID := range authenticatedUser.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.LocalPrincipalPrefix) {
			localPrincipals = append(localPrincipals, principalID)
		}
	}
	require.NotEmpty(k.T(), localPrincipals, "Enabling should have attached the Keycloak identity to the existing Rancher administrator, so the same user should still hold its local principal")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeUnrestricted, newAuthConfig.AccessMode, "Access mode should be unrestricted")

	allUsers := append(append([]authactions.User{}, k.authConfig.Users...), k.authConfig.ExcludedUsers...)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(k.T(), err, "Every Keycloak user should be able to login")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRestricted, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.Users, authactions.AccessModeRestricted+" access mode", true)
	require.NoError(k.T(), err, "Members of the allowed group should be able to login")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLRestrictedAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak SAML auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRestricted, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.ExcludedUsers, authactions.AccessModeRestricted+" access mode", false)
	require.NoError(k.T(), err, "Users outside the allowed group should NOT be able to login")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLRequiredAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRequired, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.Users, authactions.AccessModeRequired+" access mode", true)
	require.NoError(k.T(), err, "Authorized users should be able to login")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLRequiredAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak SAML auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, k.cluster.ID, k.authConfig, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, k.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRequired, principalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(k.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.ExcludedUsers, authactions.AccessModeRequired+" access mode", false)
	require.NoError(k.T(), err, "Unauthorized users should NOT be able to login")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLEnableRequiresExplicitAccessMode() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to enable Keycloak SAML")

	configuredAccessMode := k.client.Auth.KeycloakSAML.Config.AccessMode
	subSession.RegisterCleanupFunc(func() error {
		k.client.Auth.KeycloakSAML.Config.AccessMode = configuredAccessMode
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	})

	logrus.Info("Disabling Keycloak SAML so the enable path runs against a fresh provider")
	err = k.client.Auth.KeycloakSAML.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak SAML")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	k.client.Auth.KeycloakSAML.Config.AccessMode = ""

	logrus.Info("Enabling Keycloak SAML with no access mode in the request")
	err = k.client.Auth.KeycloakSAML.Enable()
	require.Error(k.T(), err, "Enabling without an access mode must be rejected so the provider can never come up open to every account in the realm")
	require.Contains(k.T(), err.Error(), authactions.NotNullableError, "Enable should be rejected because a required field was left empty; a SAML provider is enabled by writing the config resource rather than through a testAndApply action, so the schema rejects the omission rather than the action input")
	require.Contains(k.T(), err.Error(), authactions.AccessModeFieldError, "The rejected field should be accessMode rather than anything else the enable request carries")

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak SAML config")
	require.False(k.T(), keycloakConfig.Enabled, "Keycloak SAML should remain disabled after a rejected enable")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLEnableIntoRestrictedAccessModeIsNotGuarded() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to enable Keycloak SAML")

	configuredAccessMode := k.client.Auth.KeycloakSAML.Config.AccessMode
	configuredPrincipalIDs := slices.Clone(k.client.Auth.KeycloakSAML.Config.AllowedPrincipalIDs)
	subSession.RegisterCleanupFunc(func() error {
		k.client.Auth.KeycloakSAML.Config.AccessMode = configuredAccessMode
		k.client.Auth.KeycloakSAML.Config.AllowedPrincipalIDs = configuredPrincipalIDs

		if err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML); err != nil {
			return err
		}

		_, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)

		return err
	})

	logrus.Info("Disabling Keycloak SAML so the enable path runs against a fresh provider")
	err = k.client.Auth.KeycloakSAML.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak SAML")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	k.client.Auth.KeycloakSAML.Config.AccessMode = authactions.AccessModeRestricted
	k.client.Auth.KeycloakSAML.Config.AllowedPrincipalIDs = nil

	logrus.Info("Enabling Keycloak SAML directly into restricted access mode with an allow list naming nobody")
	err = k.client.Auth.KeycloakSAML.Enable()
	require.NoError(k.T(), err, "Enabling a SAML provider writes the auth config, and no login happens on that path for Rancher to check access against, so nothing stops the provider coming up restricted to an allow list naming nobody; OpenLDAP and Active Directory are enabled by a testAndApply action that signs the admin in and rejects the same request with %v", authactions.PermissionDeniedError)

	keycloakConfig, err := k.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to retrieve Keycloak SAML config")
	require.True(k.T(), keycloakConfig.Enabled, "Keycloak SAML should be enabled after an accepted enable")
	require.Equal(k.T(), authactions.AccessModeRestricted, keycloakConfig.AccessMode, "The provider should have come up in the access mode it was enabled with")
	require.NotContains(k.T(), keycloakConfig.AllowedPrincipalIDs, k.adminPrincipalID, "Enabling adds no principal of its own, so the administrator who enabled the provider is named nowhere in the allow list that now governs it")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	for _, userInfo := range k.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(k.client, groupPrincipalID, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to get group CRTB")
	require.NotNil(k.T(), foundCRTB, "Cluster role binding should exist for group")

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, k.cluster.ID)
	require.NoError(k.T(), err, "Failed to create project and namespace")

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)

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
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(k.T(), err, "User [%v] should be able to get project %s because the assertion places them in group [%v]", userInfo.Username, projectResp.Name, k.authConfig.Group)
	}

	err = authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(groupPRTBResp.Namespace, groupPRTBResp.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete PRTB: %s/%s", groupPRTBResp.Namespace, groupPRTBResp.Name)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak SAML auth input must list users outside the allowed group to prove the group binding does not reach them")

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range k.authConfig.ExcludedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(k.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)
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
		userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, userInfo)
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
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(k.T(), k.authConfig.ExcludedUsers, "Keycloak SAML auth input must list a user outside the allowed group, since this test admits one on a project binding alone")

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)
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
	outsiderPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, outsider)

	logrus.Infof("Signing user [%v] in while access is still unrestricted, so that a Rancher user record exists for them", outsider.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{outsider}, "unrestricted access mode", true)
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
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	allowedPrincipalIDs := []string{groupPrincipalID, k.adminPrincipalID}

	logrus.Infof("Restricting Keycloak SAML to group [%v] and the administrator, leaving user [%v] listed nowhere", k.authConfig.Group, outsider.Username)
	newAuthConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")
	require.ElementsMatch(k.T(), allowedPrincipalIDs, newAuthConfig.AllowedPrincipalIDs, "Allowed principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.Users, "restricted access mode as members of an allowed group", true)
	require.NoError(k.T(), err, "Members of the allowed group should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{outsider}, "restricted access mode as a project member only", true)
	require.NoError(k.T(), err, "Restricted access mode should admit user [%v] on their project binding alone, even though no principal of theirs is in the allow list", outsider.Username)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLRequiredModeRevokedPrincipalLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	grantedPrincipalIDs := []string{
		authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group),
		k.adminPrincipalID,
	}
	for _, user := range k.authConfig.Users {
		grantedPrincipalIDs = append(grantedPrincipalIDs, authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, user))
	}

	logrus.Infof("Granting Keycloak group [%v] and its members access in required mode", k.authConfig.Group)
	grantedConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRequired, grantedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to update access mode")
	require.Equal(k.T(), authactions.AccessModeRequired, grantedConfig.AccessMode, "Access mode should be required")
	require.ElementsMatch(k.T(), grantedPrincipalIDs, grantedConfig.AllowedPrincipalIDs, "Granted principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.Users, "required access mode with principals granted", true)
	require.NoError(k.T(), err, "Granted users should be able to login")

	revokedPrincipalIDs := []string{k.adminPrincipalID}

	logrus.Infof("Revoking Keycloak group [%v] and its members, leaving only the administrator allowed", k.authConfig.Group)
	revokedConfig, err := authactions.UpdateAccessMode(k.client, authactions.KeycloakSAML, authactions.AccessModeRequired, revokedPrincipalIDs)
	require.NoError(k.T(), err, "Failed to revoke principals")
	require.Equal(k.T(), authactions.AccessModeRequired, revokedConfig.AccessMode, "Access mode should remain required")
	require.ElementsMatch(k.T(), revokedPrincipalIDs, revokedConfig.AllowedPrincipalIDs, "Revoked principals should no longer appear in the allow list")

	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, k.authConfig.Users, "required access mode after principals revoked", false)
	require.NoError(k.T(), err, "Revoked users should NOT be able to login")

	stillAllowed := []authactions.User{{Username: k.adminUser.Username, Password: k.adminUser.Password}}
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, stillAllowed, "required access mode after principals revoked", true)
	require.NoError(k.T(), err, "The administrator, whose principal was left in the allow list, should still be able to login")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchFindsGroups() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	logrus.Infof("Searching principals for Keycloak group [%v]", groupName)
	expectedPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedPrincipalID)
	require.NoError(k.T(), err, "Group [%v] should be returned by principal search", groupName)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchFindsUsers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(k.authConfig.Users, k.authConfig.ExcludedUsers)
	for _, userInfo := range allUsers {
		principalName := authactions.PrincipalNameOf(userInfo)
		logrus.Infof("Searching principals for Keycloak user [%v] by the name their principal is built from, [%v]", userInfo.Username, principalName)
		expectedPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, userInfo)

		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, principalName, expectedPrincipalID)
		require.NoError(k.T(), err, "User [%v] should be returned by principal search", userInfo.Username)
	}
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchByPrincipalType() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	expectedGroupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, groupName)

	logrus.Infof("Searching principals for Keycloak group [%v] restricted to type [%v]", groupName, authactions.PrincipalTypeGroup)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, groupName, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(k.T(), err, "Search restricted to groups should return group [%v] and nothing of another type, even though an unrestricted search returns the same name as a user as well", groupName)

	userInfo := k.authConfig.Users[0]
	principalName := authactions.PrincipalNameOf(userInfo)
	expectedUserPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, userInfo)

	logrus.Infof("Searching principals for Keycloak user [%v] restricted to type [%v]", userInfo.Username, authactions.PrincipalTypeUser)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, principalName, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(k.T(), err, "Search restricted to users should return user [%v] and nothing of another type", userInfo.Username)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchByPartialName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	require.NotEmpty(k.T(), groupName, "Group name should be set in the auth configuration")
	groupPrefix := groupName[:len(groupName)/2+1]

	logrus.Infof("Searching principals for Keycloak group [%v] using partial name [%v]", groupName, groupPrefix)
	prefixPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, groupPrefix)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, prefixPrincipalID)
	require.NoError(k.T(), err, "A SAML principal search returns the term it was given, so partial name [%v] should come back as a principal in its own right", groupPrefix)

	fullPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, fullPrincipalID)
	require.Error(k.T(), err, "Keycloak SAML has no directory to search, so partial name [%v] must not resolve to group [%v]; anyone treating this search as a lookup would bind principal [%v], which matches nobody", groupPrefix, groupName, prefixPrincipalID)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchEchoesUnknownNames() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	unknownName := "keycloak-saml-no-such-principal"
	expectedPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, authactions.User{Username: unknownName})

	logrus.Infof("Searching principals for [%v], which exists nowhere in the Keycloak realm", unknownName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, unknownName, expectedPrincipalID)
	require.NoError(k.T(), err, "A SAML principal search reaches no directory and answers from the term alone, so [%v] comes back as a principal despite matching no account; a search result is therefore not evidence that a principal exists", unknownName)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalByIDResolvesGroupAndUser() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)

	logrus.Infof("Resolving Keycloak group principal [%v] by ID", groupPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.KeycloakSAML, authactions.PrincipalTypeGroup)
	require.NoError(k.T(), err, "Group principal [%v] should resolve by ID", groupPrincipalID)

	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, k.authConfig.Users[0])

	logrus.Infof("Resolving Keycloak user principal [%v] by ID", userPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.KeycloakSAML, authactions.PrincipalTypeUser)
	require.NoError(k.T(), err, "User principal [%v] should resolve by ID", userPrincipalID)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchFindsLocalUserByDisplayName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Creating a local user whose display name differs from its username")
	localUser, err := userapi.CreateUser(authAdmin)
	require.NoError(k.T(), err, "Failed to create local user")
	require.NotEqual(k.T(), localUser.Username, localUser.DisplayName, "Local user display name must differ from its username for this search to be meaningful")

	logrus.Infof("Searching principals for local user by username [%v]", localUser.Username)
	err = authactions.VerifyPrincipalIsLocal(k.client, localUser.Username)
	require.NoError(k.T(), err, "Local user [%v] should be findable by username while Keycloak SAML is enabled", localUser.Username)

	logrus.Infof("Searching principals for local user by display name [%v]", localUser.DisplayName)
	err = authactions.VerifyPrincipalIsLocal(k.client, localUser.DisplayName)
	require.NoError(k.T(), err, "Local user [%v] should be findable by display name [%v]", localUser.Username, localUser.DisplayName)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchProvisionedUserIsNotLocal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userInfo := k.authConfig.Users[0]
	logrus.Infof("Logging in as Keycloak user [%v] to provision the Rancher user", userInfo.Username)
	user := &v3.User{
		Username: userInfo.Username,
		Password: userInfo.Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

	provisionedUser, err := k.client.WranglerContext.Mgmt.User().Get(userClient.UserID, metav1.GetOptions{})
	require.NoError(k.T(), err, "Failed to retrieve the Rancher user provisioned for [%v]", userInfo.Username)
	logrus.Infof("Keycloak user [%v] provisioned Rancher user [%v] with display name [%v], username [%v] and principals %v",
		userInfo.Username, provisionedUser.Name, provisionedUser.DisplayName, provisionedUser.Username, provisionedUser.PrincipalIDs)

	require.Empty(k.T(), provisionedUser.Username, "Externally provisioned user [%v] should carry no local login username", userInfo.Username)
	require.Greater(k.T(), len(provisionedUser.PrincipalIDs), 1, "Externally provisioned user [%v] should carry an external principal alongside the local one", userInfo.Username)

	principalName := authactions.PrincipalNameOf(userInfo)
	expectedPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, userInfo)

	logrus.Infof("Searching principals for provisioned user [%v] using term [%v]", userInfo.Username, principalName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, principalName, expectedPrincipalID)
	require.NoError(k.T(), err, "Provisioned user [%v] should stay findable when searching [%v]", userInfo.Username, principalName)

	err = authactions.VerifyPrincipalNotLocal(authAdmin, principalName)
	require.NoError(k.T(), err, "Provisioned user [%v] should not surface as a local principal when searching [%v]", userInfo.Username, principalName)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLPrincipalSearchAfterProviderDisabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := k.authConfig.Group
	expectedGroupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, groupName)

	logrus.Infof("Confirming Keycloak group [%v] is returned while the provider is enabled", groupName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedGroupPrincipalID)
	require.NoError(k.T(), err, "Group [%v] should be returned while Keycloak SAML is enabled", groupName)

	logrus.Info("Disabling Keycloak SAML before searching principals again")
	err = k.client.Auth.KeycloakSAML.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak SAML")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	logrus.Info("Waiting for the SAML session that found the group to stop being accepted")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(k.T(), err, "Disabling the provider should delete the tokens it issued, so the session that reached the Keycloak principals should stop working")

	logrus.Infof("Searching principals for [%v] with Keycloak SAML disabled", groupName)
	err = authactions.VerifyPrincipalSearchExcludesProvider(k.client, groupName, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "No Keycloak principal should be returned while the provider is disabled")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLLoginAttachesExternalPrincipal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, keycloakUser)

	logrus.Infof("Logging in as Keycloak user %s so that the login flow attaches its external principal", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.Contains(k.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(k.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLDisableRemovesExternalPrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	keycloakUser := k.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.KeycloakSAML, keycloakUser)

	logrus.Infof("Logging in as Keycloak user %s so that a user record carrying its external principal exists", keycloakUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.KeycloakSAML, []authactions.User{keycloakUser}, "external principal attachment", true)
	require.NoError(k.T(), err, "Keycloak user should be able to login")

	attachedUser, err := userapi.WaitForUserByPrincipalID(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Login should attach the external principal to a user record")
	require.ElementsMatch(k.T(), []string{userPrincipalID, authactions.LocalPrincipalPrefix + attachedUser.Name}, attachedUser.PrincipalIDs,
		"User %s should carry only the Keycloak principal and the local principal added alongside it, so that disabling the provider leaves it with no external identity", attachedUser.Name)

	logrus.Info("Disabling Keycloak SAML so that the cleanup service reconciles principals it emitted")
	err = k.client.Auth.KeycloakSAML.Disable()
	require.NoError(k.T(), err, "Failed to disable Keycloak SAML")

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling Keycloak SAML so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(k.client, authactions.KeycloakSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(k.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(k.client, userPrincipalID)
	require.NoError(k.T(), err, "Disabling the provider should leave no user carrying a Keycloak principal")

	logrus.Infof("Verifying that user %s was removed rather than left behind holding only its local principal", attachedUser.Name)
	err = userapi.WaitForUserDeletion(k.client, attachedUser.Name)
	require.NoError(k.T(), err, "Disabling the provider should remove a user whose only external identity that provider emitted, leaving no record that could later be rebound")
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLNestedGroupsInAssertion() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to enable Keycloak SAML")

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
		logrus.Infof("Reading the groups Keycloak asserts for [%v], a member of group [%v] %v", tier.user.Username, tier.group, tier.description)
		groups, err := authactions.KeycloakSAMLAssertionGroups(k.client, tier.user)
		require.NoError(k.T(), err, "Failed to capture the assertion Keycloak issues for user [%v]", tier.user.Username)

		require.Contains(k.T(), groups, tier.group, "Keycloak should assert the group [%v] user [%v] belongs to directly, otherwise no binding of any kind can reach them", tier.group, tier.user.Username)

		for _, ancestor := range tier.ancestors {
			require.NotContains(k.T(), groups, ancestor, "Keycloak asserts direct memberships alone, so ancestor group [%v] must not appear for user [%v]; Rancher's SAML provider resolves no hierarchy of its own and believes this list verbatim, so anything absent here is invisible to Rancher", ancestor, tier.user.Username)
		}
	}
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLParentGroupBindingDoesNotReachNestedMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.Group, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding")

	directMember := &v3.User{
		Username: k.authConfig.Users[0].Username,
		Password: k.authConfig.Users[0].Password,
	}
	directClient, err := authactions.LoginAsAuthUser(authAdmin, directMember, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to login user [%v]", directMember.Username)

	rbac.VerifyUserCanListCluster(k.T(), k.client, directClient, k.cluster.ID, rbac.ClusterOwner)

	nestedMembers := append(slices.Clone(k.authConfig.NestedUsers), k.authConfig.DoubleNestedUsers...)

	for _, userInfo := range nestedMembers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(k.T(), err, "User [%v] sits beneath group [%v] rather than in it, and Keycloak asserts only their direct group, so the binding on [%v] must not reach them; OpenLDAP and Active Directory do resolve nested membership here, which is where a migration to Keycloak SAML silently loses access", user.Username, k.authConfig.Group, groupPrincipalID)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLNestedGroupBindingGrantsItsOwnMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	nestedPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, k.authConfig.NestedGroup)

	logrus.Infof("Granting nested Keycloak group [%v] the %v role on cluster [%v]", k.authConfig.NestedGroup, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, nestedPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "A nested group should be bindable by its own name, since Rancher takes group names off the assertion and never asks Keycloak where they sit")

	for _, userInfo := range k.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	for _, userInfo := range k.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(k.T(), err, "User [%v] belongs to group [%v] beneath [%v], and a nested group is as flat to Rancher as any other, so the binding must not descend to them either", user.Username, k.authConfig.DoubleNestedGroup, k.authConfig.NestedGroup)
		require.Contains(k.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (k *KeycloakSAMLAuthProviderSuite) TestKeycloakSAMLFullGroupPathBinding() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakSAML)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Asking Keycloak to name each group by its full path rather than by its bare name")
	restoreGroupPathMode, err := authactions.SetKeycloakSAMLGroupPathMode(k.keycloak, k.entityID, true)
	require.NoError(k.T(), err, "Failed to switch the Keycloak groups mapper to full paths")
	subSession.RegisterCleanupFunc(restoreGroupPathMode)

	nestedPath := fmt.Sprintf("/%s/%s", k.authConfig.Group, k.authConfig.NestedGroup)
	doubleNestedPath := fmt.Sprintf("%s/%s", nestedPath, k.authConfig.DoubleNestedGroup)

	pathTiers := []struct {
		user     authactions.User
		path     string
		bareName string
	}{
		{k.authConfig.NestedUsers[0], nestedPath, k.authConfig.NestedGroup},
		{k.authConfig.DoubleNestedUsers[0], doubleNestedPath, k.authConfig.DoubleNestedGroup},
	}

	for _, tier := range pathTiers {
		groups, err := authactions.KeycloakSAMLAssertionGroups(k.client, tier.user)
		require.NoError(k.T(), err, "Failed to capture the assertion Keycloak issues for user [%v]", tier.user.Username)

		require.Contains(k.T(), groups, tier.path, "Keycloak should now assert group [%v] as the path [%v], which is what makes a nested group distinguishable from any other group sharing its name", tier.bareName, tier.path)
		require.NotContains(k.T(), groups, tier.bareName, "The bare name [%v] should no longer appear, so a binding written against it stops granting anything the moment this setting changes", tier.bareName)
	}

	pathPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.KeycloakSAML, nestedPath)

	logrus.Infof("Granting the group at path [%v] the %v role on cluster [%v]", nestedPath, rbac.ClusterOwner, k.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.cluster.ID, pathPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to create cluster role template binding for principal [%v]", pathPrincipalID)

	for _, userInfo := range k.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.KeycloakSAML)
		require.NoError(k.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(k.T(), k.client, userClient, k.cluster.ID, rbac.ClusterOwner)
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(k.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func TestKeycloakSAMLAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(KeycloakSAMLAuthProviderSuite))
}

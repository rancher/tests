//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package genericsamlkeycloak

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/keycloak"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/saml"
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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type GenericSAMLKeycloakAuthProviderSuite struct {
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

func (g *GenericSAMLKeycloakAuthProviderSuite) SetupSuite() {
	g.session = session.NewSession()
	g.keycloakSession = session.NewSession()

	client, err := rancher.NewClient("", g.session)
	require.NoError(g.T(), err, "Failed to create Rancher client")
	g.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(g.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(g.client, clusterName)
	require.NoError(g.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	g.cluster, err = g.client.Management.Cluster.ByID(clusterID)
	require.NoError(g.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Connecting to Keycloak as a realm administrator")
	g.keycloak, err = authactions.NewKeycloakClient(g.keycloakSession)
	require.NoError(g.T(), err, "Failed to create Keycloak admin client")

	logrus.Info("Registering the Rancher generic SAML client and settling the test accounts in the Keycloak realm")
	genericSAMLKeycloakFixture, err := authactions.SetupGenericSAMLKeycloak(g.client, g.keycloak)
	require.NoError(g.T(), err, "Failed to set up the Keycloak realm for generic SAML")

	g.authConfig = genericSAMLKeycloakFixture.AuthInput
	require.NotEmpty(g.T(), g.authConfig.Group, "Generic SAML setup should have settled on the allowed group")
	require.NotEmpty(g.T(), g.authConfig.Users, "Generic SAML setup should have settled on the users to sign in as")
	require.NotEmpty(g.T(), g.authConfig.ExcludedUsers, "Generic SAML setup should have settled on a user outside the allowed group")
	require.NotEmpty(g.T(), g.authConfig.NestedGroup, "Generic SAML setup should have settled on a group nested beneath the allowed group")
	require.NotEmpty(g.T(), g.authConfig.NestedUsers, "Generic SAML setup should have settled on a member of the nested group")
	require.NotEmpty(g.T(), g.authConfig.DoubleNestedGroup, "Generic SAML setup should have settled on a group nested two deep")
	require.NotEmpty(g.T(), g.authConfig.DoubleNestedUsers, "Generic SAML setup should have settled on a member of the doubly nested group")

	g.adminUser = &v3.User{
		Username: genericSAMLKeycloakFixture.Admin.Username,
		Password: genericSAMLKeycloakFixture.Admin.Password,
	}
	g.adminPrincipalID = genericSAMLKeycloakFixture.AdminPrincipalID
	g.entityID = genericSAMLKeycloakFixture.EntityID
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TearDownSuite() {
	defer g.keycloakSession.Cleanup()
	defer g.session.Cleanup()

	if g.client != nil {
		genericSAMLConfig, err := g.client.Management.AuthConfig.ByID(authactions.GenericSAML)
		if err == nil && genericSAMLConfig.Enabled {
			logrus.Info("Disabling generic SAML authentication after test suite")
			err := g.client.Auth.GenericSAML.Disable()
			require.NoError(g.T(), err, "Failed to disable generic SAML in teardown")
		}
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakEnableProvider() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to enable generic SAML")

	genericSAMLConfig, err := g.client.Management.AuthConfig.ByID(authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to retrieve generic SAML config")

	require.True(g.T(), genericSAMLConfig.Enabled, "Generic SAML should be enabled")
	require.Equal(g.T(), v3.GenericSAMLConfigType, genericSAMLConfig.Type, "Auth config should be stored as the generic SAML subtype")
	require.Equal(g.T(), authactions.AuthProvCleanupAnnotationValUnlocked, genericSAMLConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")

	secret, err := g.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.GenericSAMLKeySecretID,
		metav1.GetOptions{},
	)
	require.NoError(g.T(), err, "Rancher should move the service provider signing key out of the auth config into a secret")
	require.NotEmpty(g.T(), secret.Data, "Signing key secret should hold the key")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakDisableAndReenableProvider() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to enable generic SAML")

	err = g.client.Auth.GenericSAML.Disable()
	require.NoError(g.T(), err, "Failed to disable generic SAML")

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling generic SAML so that a failure below does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	genericSAMLConfig, err := authactions.WaitForAuthProviderAnnotationUpdate(g.client, authactions.GenericSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(g.T(), err, "Failed waiting for annotation update")

	require.False(g.T(), genericSAMLConfig.Enabled, "Generic SAML should be disabled")
	require.Equal(g.T(), authactions.AuthProvCleanupAnnotationValLocked, genericSAMLConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be locked")

	_, err = g.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.GenericSAMLKeySecretID,
		metav1.GetOptions{},
	)
	require.Error(g.T(), err, "Signing key secret should be removed when the provider is disabled")
	require.True(g.T(), apierrors.IsNotFound(err), "expected NotFound error, got: %v", err)

	logrus.Info("Re-enabling generic SAML through an admin login to confirm a disable leaves nothing behind that blocks it")
	err = authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to re-enable generic SAML")

	reenabledConfig, err := g.client.Management.AuthConfig.ByID(authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to retrieve generic SAML config after re-enabling")
	require.True(g.T(), reenabledConfig.Enabled, "Generic SAML should be enabled again")
	require.Equal(g.T(), authactions.AuthProvCleanupAnnotationValUnlocked, reenabledConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked again")

	reenabledSecret, err := g.client.WranglerContext.Core.Secret().Get(
		rbac.GlobalDataNS,
		authactions.GenericSAMLKeySecretID,
		metav1.GetOptions{},
	)
	require.NoError(g.T(), err, "Re-enabling should recreate the service provider signing key secret that disabling removed")
	require.NotEmpty(g.T(), reenabledSecret.Data, "Recreated signing key secret should hold the key")

	logrus.Info("Logging in as a generic SAML user to confirm the provider authenticates after the disable and re-enable cycle")
	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "provider re-enabled after a disable", true)
	require.NoError(g.T(), err, "Generic SAML users should be able to login after the provider is re-enabled")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakAdminLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	authenticatedUser, err := authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(g.T(), err, "Failed to retrieve the user the SAML session authenticated as")
	require.Contains(g.T(), authenticatedUser.PrincipalIDs, g.adminPrincipalID, "Session should resolve to the generic SAML principal")

	var localPrincipals []string
	for _, principalID := range authenticatedUser.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.LocalPrincipalPrefix) {
			localPrincipals = append(localPrincipals, principalID)
		}
	}
	require.NotEmpty(g.T(), localPrincipals, "Enabling should have attached the generic SAML identity to the existing Rancher administrator, so the same user should still hold its local principal")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	newAuthConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
	require.NoError(g.T(), err, "Failed to update access mode")
	require.Equal(g.T(), authactions.AccessModeUnrestricted, newAuthConfig.AccessMode, "Access mode should be unrestricted")

	allUsers := append(append([]authactions.User{}, g.authConfig.Users...), g.authConfig.ExcludedUsers...)
	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(g.T(), err, "Every generic SAML user should be able to login")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, g.cluster.ID, g.authConfig, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, g.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRestricted, principalIDs)
	require.NoError(g.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(g.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.Users, authactions.AccessModeRestricted+" access mode", true)
	require.NoError(g.T(), err, "Members of the allowed group should be able to login")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakRestrictedAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(g.T(), g.authConfig.ExcludedUsers, "Generic SAML auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, g.cluster.ID, g.authConfig, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, g.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRestricted, principalIDs)
	require.NoError(g.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(g.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.ExcludedUsers, authactions.AccessModeRestricted+" access mode", false)
	require.NoError(g.T(), err, "Users outside the allowed group should NOT be able to login")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakRequiredAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, g.cluster.ID, g.authConfig, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, g.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRequired, principalIDs)
	require.NoError(g.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(g.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.Users, authactions.AccessModeRequired+" access mode", true)
	require.NoError(g.T(), err, "Authorized users should be able to login")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakRequiredAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(g.T(), g.authConfig.ExcludedUsers, "Generic SAML auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupSAMLRequiredAccessModePrincipals(authAdmin, g.cluster.ID, g.authConfig, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, g.adminPrincipalID)

	newAuthConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRequired, principalIDs)
	require.NoError(g.T(), err, "Failed to update access mode")
	subSession.RegisterCleanupFunc(func() error {
		_, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
		return err
	})
	require.Equal(g.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.ExcludedUsers, authactions.AccessModeRequired+" access mode", false)
	require.NoError(g.T(), err, "Unauthorized users should NOT be able to login")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakEnableRequiresExplicitAccessMode() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to enable generic SAML")

	configuredAccessMode := g.client.Auth.GenericSAML.Config.AccessMode
	subSession.RegisterCleanupFunc(func() error {
		g.client.Auth.GenericSAML.Config.AccessMode = configuredAccessMode
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	logrus.Info("Disabling generic SAML so the enable path runs against a fresh provider")
	err = g.client.Auth.GenericSAML.Disable()
	require.NoError(g.T(), err, "Failed to disable generic SAML")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(g.client, authactions.GenericSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(g.T(), err, "Failed waiting for annotation update")

	g.client.Auth.GenericSAML.Config.AccessMode = ""

	logrus.Info("Enabling generic SAML with no access mode in the request")
	err = g.client.Auth.GenericSAML.Enable()
	require.Error(g.T(), err, "Enabling without an access mode must be rejected so the provider can never come up open to every account in the realm")
	require.Contains(g.T(), err.Error(), authactions.NotNullableError, "Enable should be rejected because the required field accessMode was omitted")
	require.Contains(g.T(), err.Error(), authactions.AccessModeFieldError, "The rejected field should be accessMode rather than anything else the enable request carries")

	genericSAMLConfig, err := g.client.Management.AuthConfig.ByID(authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to retrieve generic SAML config")
	require.False(g.T(), genericSAMLConfig.Enabled, "Generic SAML should remain disabled after a rejected enable")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakEnableIntoRestrictedAccessModeIsNotGuarded() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to enable generic SAML")

	configuredAccessMode := g.client.Auth.GenericSAML.Config.AccessMode
	configuredPrincipalIDs := slices.Clone(g.client.Auth.GenericSAML.Config.AllowedPrincipalIDs)
	subSession.RegisterCleanupFunc(func() error {
		g.client.Auth.GenericSAML.Config.AccessMode = configuredAccessMode
		g.client.Auth.GenericSAML.Config.AllowedPrincipalIDs = configuredPrincipalIDs

		if err := authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML); err != nil {
			return err
		}

		_, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)

		return err
	})

	logrus.Info("Disabling generic SAML so the enable path runs against a fresh provider")
	err = g.client.Auth.GenericSAML.Disable()
	require.NoError(g.T(), err, "Failed to disable generic SAML")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(g.client, authactions.GenericSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(g.T(), err, "Failed waiting for annotation update")

	g.client.Auth.GenericSAML.Config.AccessMode = authactions.AccessModeRestricted
	g.client.Auth.GenericSAML.Config.AllowedPrincipalIDs = nil

	logrus.Info("Enabling generic SAML directly into restricted access mode with an empty allow list")
	err = g.client.Auth.GenericSAML.Enable()
	require.NoError(g.T(), err, "Enabling a SAML provider writes the auth config, and no login happens on that path for Rancher to check access against, so nothing stops the provider coming up restricted to an empty allow list")

	genericSAMLConfig, err := g.client.Management.AuthConfig.ByID(authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to retrieve generic SAML config")
	require.True(g.T(), genericSAMLConfig.Enabled, "Generic SAML should be enabled after an accepted enable")
	require.Equal(g.T(), authactions.AccessModeRestricted, genericSAMLConfig.AccessMode, "The provider should have come up in the access mode it was enabled with")
	require.NotContains(g.T(), genericSAMLConfig.AllowedPrincipalIDs, g.adminPrincipalID, "Enabling adds no principal of its own, so the administrator who enabled the provider is named nowhere in the allow list that now governs it")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", g.authConfig.Group, rbac.ClusterOwner, g.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(g.T(), err, "Failed to create cluster role template binding")

	for _, userInfo := range g.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(g.T(), g.client, userClient, g.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(g.client, groupPrincipalID, g.cluster.ID)
	require.NoError(g.T(), err, "Failed to get group CRTB")
	require.NotNil(g.T(), foundCRTB, "Cluster role binding should exist for group")

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(g.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, g.cluster.ID)
	require.NoError(g.T(), err, "Failed to create project and namespace")

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	logrus.Infof("Granting Keycloak group [%v] the %v role on project [%v]", g.authConfig.Group, rbac.ProjectOwner, projectName)
	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, groupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(g.T(), err, "Failed to create PRTB")
	require.NotNil(g.T(), groupPRTBResp, "PRTB should be created")

	for _, userInfo := range g.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(g.T(), err, "User [%v] should be able to get project %s because the assertion places them in group [%v]", userInfo.Username, projectResp.Name, g.authConfig.Group)
	}

	err = authAdmin.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(groupPRTBResp.Namespace, groupPRTBResp.Name, &metav1.DeleteOptions{})
	require.NoError(g.T(), err, "Failed to delete PRTB: %s/%s", groupPRTBResp.Namespace, groupPRTBResp.Name)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(g.T(), g.authConfig.ExcludedUsers, "Generic SAML auth input must list users outside the allowed group to prove the group binding does not reach them")

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", g.authConfig.Group, rbac.ClusterOwner, g.cluster.ID)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(g.T(), err, "Failed to create group cluster role template binding")

	for _, userInfo := range g.authConfig.ExcludedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.NotNil(g.T(), err, "User [%v] should NOT list clusters", userInfo.Username)
		require.Contains(g.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(g.T(), err, "Failed to create cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, g.cluster.ID)
	require.NoError(g.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(g.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	for _, userInfo := range g.authConfig.Users {
		userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, userInfo)
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
		require.NoError(g.T(), err, "Failed to create PRTB for user [%v]", userInfo.Username)
		require.NotNil(g.T(), userPRTBResp, "PRTB should be created for user [%v]", userInfo.Username)
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(g.T(), g.authConfig.ExcludedUsers, "Generic SAML auth input must list a user outside the allowed group, since this test admits one on a project binding alone")

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(g.T(), err, "Failed to create group cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, g.cluster.ID)
	require.NoError(g.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(g.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	outsider := g.authConfig.ExcludedUsers[0]
	outsiderPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, outsider)

	logrus.Infof("Signing user [%v] in while access is still unrestricted, so that a Rancher user record exists for them", outsider.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, []authactions.User{outsider}, "unrestricted access mode", true)
	require.NoError(g.T(), err, "User [%v] should be able to login before the provider is restricted; restricted access mode reads a user's bindings off their Rancher user record, which only a login creates", outsider.Username)

	_, err = userapi.WaitForUserByPrincipalID(g.client, outsiderPrincipalID)
	require.NoError(g.T(), err, "Login should have created a Rancher user record carrying principal [%v]", outsiderPrincipalID)

	logrus.Infof("Granting user [%v], who is outside group [%v], the %v role on project [%v]", outsider.Username, g.authConfig.Group, rbac.ProjectOwner, projectName)
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
	require.NoError(g.T(), err, "Failed to create PRTB for user [%v]", outsider.Username)
	require.NotNil(g.T(), outsiderPRTBResp, "PRTB should be created for user [%v]", outsider.Username)

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	allowedPrincipalIDs := []string{groupPrincipalID, g.adminPrincipalID}

	logrus.Infof("Restricting generic SAML to group [%v] and the administrator, leaving user [%v] listed nowhere", g.authConfig.Group, outsider.Username)
	newAuthConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(g.T(), err, "Failed to update access mode")
	require.Equal(g.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")
	require.ElementsMatch(g.T(), allowedPrincipalIDs, newAuthConfig.AllowedPrincipalIDs, "Allowed principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.Users, "restricted access mode as members of an allowed group", true)
	require.NoError(g.T(), err, "Members of the allowed group should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, []authactions.User{outsider}, "restricted access mode as a project member only", true)
	require.NoError(g.T(), err, "Restricted access mode should admit user [%v] on their project binding alone, even though no principal of theirs is in the allow list", outsider.Username)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakRequiredModeRevokedPrincipalLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		_, rollbackErr := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeUnrestricted, nil)
		return rollbackErr
	})

	grantedPrincipalIDs := []string{
		authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group),
		g.adminPrincipalID,
	}
	for _, user := range g.authConfig.Users {
		grantedPrincipalIDs = append(grantedPrincipalIDs, authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, user))
	}

	logrus.Infof("Granting Keycloak group [%v] and its members access in required mode", g.authConfig.Group)
	grantedConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRequired, grantedPrincipalIDs)
	require.NoError(g.T(), err, "Failed to update access mode")
	require.Equal(g.T(), authactions.AccessModeRequired, grantedConfig.AccessMode, "Access mode should be required")
	require.ElementsMatch(g.T(), grantedPrincipalIDs, grantedConfig.AllowedPrincipalIDs, "Granted principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.Users, "required access mode with principals granted", true)
	require.NoError(g.T(), err, "Granted users should be able to login")

	revokedPrincipalIDs := []string{g.adminPrincipalID}

	logrus.Infof("Revoking Keycloak group [%v] and its members, leaving only the administrator allowed", g.authConfig.Group)
	revokedConfig, err := authactions.UpdateAccessMode(g.client, authactions.GenericSAML, authactions.AccessModeRequired, revokedPrincipalIDs)
	require.NoError(g.T(), err, "Failed to revoke principals")
	require.Equal(g.T(), authactions.AccessModeRequired, revokedConfig.AccessMode, "Access mode should remain required")
	require.ElementsMatch(g.T(), revokedPrincipalIDs, revokedConfig.AllowedPrincipalIDs, "Revoked principals should no longer appear in the allow list")

	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, g.authConfig.Users, "required access mode after principals revoked", false)
	require.NoError(g.T(), err, "Revoked users should NOT be able to login")

	stillAllowed := []authactions.User{{Username: g.adminUser.Username, Password: g.adminUser.Password}}
	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, stillAllowed, "required access mode after principals revoked", true)
	require.NoError(g.T(), err, "The administrator, whose principal was left in the allow list, should still be able to login")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchFindsGroups() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := g.authConfig.Group
	logrus.Infof("Searching principals for Keycloak group [%v]", groupName)
	expectedPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedPrincipalID)
	require.NoError(g.T(), err, "Group [%v] should be returned by principal search", groupName)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchFindsUsers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(g.authConfig.Users, g.authConfig.ExcludedUsers)
	for _, userInfo := range allUsers {
		principalName := authactions.PrincipalNameOf(userInfo)
		logrus.Infof("Searching principals for Keycloak user [%v] by the name their principal is built from, [%v]", userInfo.Username, principalName)
		expectedPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, userInfo)

		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, principalName, expectedPrincipalID)
		require.NoError(g.T(), err, "User [%v] should be returned by principal search", userInfo.Username)
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchByPrincipalType() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := g.authConfig.Group
	expectedGroupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, groupName)

	logrus.Infof("Searching principals for Keycloak group [%v] restricted to type [%v]", groupName, authactions.PrincipalTypeGroup)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, groupName, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(g.T(), err, "Search restricted to groups should return group [%v] and nothing of another type, even though an unrestricted search returns the same name as a user as well", groupName)

	userInfo := g.authConfig.Users[0]
	principalName := authactions.PrincipalNameOf(userInfo)
	expectedUserPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, userInfo)

	logrus.Infof("Searching principals for Keycloak user [%v] restricted to type [%v]", userInfo.Username, authactions.PrincipalTypeUser)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, principalName, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(g.T(), err, "Search restricted to users should return user [%v] and nothing of another type", userInfo.Username)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchByPartialName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := g.authConfig.Group
	require.NotEmpty(g.T(), groupName, "Group name should be set in the auth configuration")
	groupPrefix := groupName[:len(groupName)/2+1]

	logrus.Infof("Searching principals for Keycloak group [%v] using partial name [%v]", groupName, groupPrefix)
	prefixPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, groupPrefix)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, prefixPrincipalID)
	require.NoError(g.T(), err, "A SAML principal search returns the term it was given, so partial name [%v] should come back as a principal in its own right", groupPrefix)

	fullPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, fullPrincipalID)
	require.Error(g.T(), err, "Generic SAML has no directory to search, so partial name [%v] must not resolve to group [%v]; anyone treating this search as a lookup would bind principal [%v], which matches nobody", groupPrefix, groupName, prefixPrincipalID)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchEchoesUnknownNames() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	unknownName := "generic-saml-no-such-principal"
	expectedPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, authactions.User{Username: unknownName})

	logrus.Infof("Searching principals for [%v], which exists nowhere in the Keycloak realm", unknownName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, unknownName, expectedPrincipalID)
	require.NoError(g.T(), err, "A SAML principal search reaches no directory and answers from the term alone, so [%v] comes back as a principal despite matching no account; a search result is therefore not evidence that a principal exists", unknownName)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalByIDResolvesGroupAndUser() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)

	logrus.Infof("Resolving generic SAML group principal [%v] by ID", groupPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.GenericSAML, authactions.PrincipalTypeGroup)
	require.NoError(g.T(), err, "Group principal [%v] should resolve by ID", groupPrincipalID)

	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, g.authConfig.Users[0])

	logrus.Infof("Resolving generic SAML user principal [%v] by ID", userPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.GenericSAML, authactions.PrincipalTypeUser)
	require.NoError(g.T(), err, "User principal [%v] should resolve by ID", userPrincipalID)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchFindsLocalUserByDisplayName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Creating a local user whose display name differs from its username")
	localUser, err := userapi.CreateUser(authAdmin)
	require.NoError(g.T(), err, "Failed to create local user")
	require.NotEqual(g.T(), localUser.Username, localUser.DisplayName, "Local user display name must differ from its username for this search to be meaningful")

	logrus.Infof("Searching principals for local user by username [%v]", localUser.Username)
	err = authactions.VerifyPrincipalIsLocal(g.client, localUser.Username)
	require.NoError(g.T(), err, "Local user [%v] should be findable by username while generic SAML is enabled", localUser.Username)

	logrus.Infof("Searching principals for local user by display name [%v]", localUser.DisplayName)
	err = authactions.VerifyPrincipalIsLocal(g.client, localUser.DisplayName)
	require.NoError(g.T(), err, "Local user [%v] should be findable by display name [%v]", localUser.Username, localUser.DisplayName)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchProvisionedUserIsNotLocal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userInfo := g.authConfig.Users[0]
	logrus.Infof("Logging in as generic SAML user [%v] to provision the Rancher user", userInfo.Username)
	user := &v3.User{
		Username: userInfo.Username,
		Password: userInfo.Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

	provisionedUser, err := g.client.WranglerContext.Mgmt.User().Get(userClient.UserID, metav1.GetOptions{})
	require.NoError(g.T(), err, "Failed to retrieve the Rancher user provisioned for [%v]", userInfo.Username)
	logrus.Infof("Generic SAML user [%v] provisioned Rancher user [%v] with display name [%v], username [%v] and principals %v",
		userInfo.Username, provisionedUser.Name, provisionedUser.DisplayName, provisionedUser.Username, provisionedUser.PrincipalIDs)

	require.Empty(g.T(), provisionedUser.Username, "Externally provisioned user [%v] should carry no local login username", userInfo.Username)
	require.Greater(g.T(), len(provisionedUser.PrincipalIDs), 1, "Externally provisioned user [%v] should carry an external principal alongside the local one", userInfo.Username)

	principalName := authactions.PrincipalNameOf(userInfo)
	expectedPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, userInfo)

	logrus.Infof("Searching principals for provisioned user [%v] using term [%v]", userInfo.Username, principalName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, principalName, expectedPrincipalID)
	require.NoError(g.T(), err, "Provisioned user [%v] should stay findable when searching [%v]", userInfo.Username, principalName)

	err = authactions.VerifyPrincipalNotLocal(authAdmin, principalName)
	require.NoError(g.T(), err, "Provisioned user [%v] should not surface as a local principal when searching [%v]", userInfo.Username, principalName)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakPrincipalSearchAfterProviderDisabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := g.authConfig.Group
	expectedGroupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, groupName)

	logrus.Infof("Confirming Keycloak group [%v] is returned while the provider is enabled", groupName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedGroupPrincipalID)
	require.NoError(g.T(), err, "Group [%v] should be returned while generic SAML is enabled", groupName)

	logrus.Info("Disabling generic SAML before searching principals again")
	err = g.client.Auth.GenericSAML.Disable()
	require.NoError(g.T(), err, "Failed to disable generic SAML")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(g.client, authactions.GenericSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(g.T(), err, "Failed waiting for annotation update")

	logrus.Info("Waiting for the SAML session that found the group to stop being accepted")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(g.T(), err, "Disabling the provider should delete the tokens it issued, so the session that reached the generic SAML principals should stop working")

	logrus.Infof("Searching principals for [%v] with generic SAML disabled", groupName)
	err = authactions.VerifyPrincipalSearchExcludesProvider(g.client, groupName, authactions.GenericSAML)
	require.NoError(g.T(), err, "No generic SAML principal should be returned while the provider is disabled")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakLoginAttachesExternalPrincipal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	genericSAMLUser := g.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, genericSAMLUser)

	logrus.Infof("Logging in as generic SAML user %s so that the login flow attaches its external principal", genericSAMLUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, []authactions.User{genericSAMLUser}, "external principal attachment", true)
	require.NoError(g.T(), err, "Generic SAML user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(g.client, userPrincipalID)
	require.NoError(g.T(), err, "Login should attach the external principal to a user record")
	require.Contains(g.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(g.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakDisableRemovesExternalPrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	genericSAMLUser := g.authConfig.Users[0]
	userPrincipalID := authactions.GetSAMLUserPrincipalID(authactions.GenericSAML, genericSAMLUser)

	logrus.Infof("Logging in as generic SAML user %s so that a user record carrying its external principal exists", genericSAMLUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.GenericSAML, []authactions.User{genericSAMLUser}, "external principal attachment", true)
	require.NoError(g.T(), err, "Generic SAML user should be able to login")

	attachedUser, err := userapi.WaitForUserByPrincipalID(g.client, userPrincipalID)
	require.NoError(g.T(), err, "Login should attach the external principal to a user record")
	require.ElementsMatch(g.T(), []string{userPrincipalID, authactions.LocalPrincipalPrefix + attachedUser.Name}, attachedUser.PrincipalIDs,
		"User %s should carry only the generic SAML principal and the local principal added alongside it, so that disabling the provider leaves it with no external identity", attachedUser.Name)

	logrus.Info("Disabling generic SAML so that the cleanup service reconciles principals it emitted")
	err = g.client.Auth.GenericSAML.Disable()
	require.NoError(g.T(), err, "Failed to disable generic SAML")

	subSession.RegisterCleanupFunc(func() error {
		logrus.Info("Re-enabling generic SAML so that a failure here does not leave the provider disabled for later tests")
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(g.client, authactions.GenericSAML, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(g.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(g.client, userPrincipalID)
	require.NoError(g.T(), err, "Disabling the provider should leave no user carrying a generic SAML principal")

	logrus.Infof("Verifying that user %s was removed rather than left behind holding only its local principal", attachedUser.Name)
	err = userapi.WaitForUserDeletion(g.client, attachedUser.Name)
	require.NoError(g.T(), err, "Disabling the provider should remove a user whose only external identity that provider emitted, leaving no record that could later be rebound")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakNestedGroupsInAssertion() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to enable generic SAML")

	tiers := []struct {
		description string
		user        authactions.User
		group       string
		ancestors   []string
	}{
		{
			description: "nested one deep",
			user:        g.authConfig.NestedUsers[0],
			group:       g.authConfig.NestedGroup,
			ancestors:   []string{g.authConfig.Group},
		},
		{
			description: "nested two deep",
			user:        g.authConfig.DoubleNestedUsers[0],
			group:       g.authConfig.DoubleNestedGroup,
			ancestors:   []string{g.authConfig.Group, g.authConfig.NestedGroup},
		},
	}

	for _, tier := range tiers {
		logrus.Infof("Reading the groups Keycloak asserts for [%v], a member of group [%v] %v", tier.user.Username, tier.group, tier.description)
		assertionGroups, err := authactions.GenericSAMLAssertionGroups(g.client, tier.user)
		require.NoError(g.T(), err, "Failed to capture the assertion Keycloak issues for user [%v]", tier.user.Username)

		require.Contains(g.T(), assertionGroups, tier.group, "Keycloak should assert the group [%v] user [%v] belongs to directly, otherwise no binding of any kind can reach them", tier.group, tier.user.Username)

		for _, ancestor := range tier.ancestors {
			require.NotContains(g.T(), assertionGroups, ancestor, "Keycloak asserts direct memberships alone, so ancestor group [%v] must not appear for user [%v]; Rancher's SAML provider resolves no hierarchy of its own and believes this list verbatim, so anything absent here is invisible to Rancher", ancestor, tier.user.Username)
		}
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakParentGroupBindingDoesNotReachNestedMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.Group)

	logrus.Infof("Granting Keycloak group [%v] the %v role on cluster [%v]", g.authConfig.Group, rbac.ClusterOwner, g.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(g.T(), err, "Failed to create cluster role template binding")

	directMember := &v3.User{
		Username: g.authConfig.Users[0].Username,
		Password: g.authConfig.Users[0].Password,
	}
	directClient, err := authactions.LoginAsAuthUser(authAdmin, directMember, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to login user [%v]", directMember.Username)

	rbac.VerifyUserCanListCluster(g.T(), g.client, directClient, g.cluster.ID, rbac.ClusterOwner)

	nestedMembers := append(slices.Clone(g.authConfig.NestedUsers), g.authConfig.DoubleNestedUsers...)

	for _, userInfo := range nestedMembers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(g.T(), err, "User [%v] sits beneath group [%v] rather than in it, and Keycloak asserts only their direct group, so the binding on [%v] must not reach them", user.Username, g.authConfig.Group, groupPrincipalID)
		require.Contains(g.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(g.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakNestedGroupBindingGrantsItsOwnMembers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	nestedPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, g.authConfig.NestedGroup)

	logrus.Infof("Granting nested Keycloak group [%v] the %v role on cluster [%v]", g.authConfig.NestedGroup, rbac.ClusterOwner, g.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, nestedPrincipalID, rbac.ClusterOwner.String())
	require.NoError(g.T(), err, "A nested group should be bindable by its own name, since Rancher takes group names off the assertion and never asks Keycloak where they sit")

	for _, userInfo := range g.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(g.T(), g.client, userClient, g.cluster.ID, rbac.ClusterOwner)
	}

	for _, userInfo := range g.authConfig.DoubleNestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
		require.Error(g.T(), err, "User [%v] belongs to group [%v] beneath [%v], and a nested group is as flat to Rancher as any other, so the binding must not descend to them either", user.Username, g.authConfig.DoubleNestedGroup, g.authConfig.NestedGroup)
		require.Contains(g.T(), err.Error(), "Resource type [provisioning.cattle.io.cluster] has no method GET", "Should indicate insufficient permissions")
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(g.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakFullGroupPathBinding() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(g.client, g.session, g.adminUser, authactions.GenericSAML)
	require.NoError(g.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Asking Keycloak to name each group by its full path rather than by its bare name")
	restoreGroupPathMode, err := authactions.SetKeycloakSAMLGroupPathMode(g.keycloak, g.entityID, true)
	require.NoError(g.T(), err, "Failed to switch the Keycloak groups mapper to full paths")
	subSession.RegisterCleanupFunc(restoreGroupPathMode)

	nestedPath := fmt.Sprintf("/%s/%s", g.authConfig.Group, g.authConfig.NestedGroup)
	doubleNestedPath := fmt.Sprintf("%s/%s", nestedPath, g.authConfig.DoubleNestedGroup)

	pathTiers := []struct {
		user     authactions.User
		path     string
		bareName string
	}{
		{g.authConfig.NestedUsers[0], nestedPath, g.authConfig.NestedGroup},
		{g.authConfig.DoubleNestedUsers[0], doubleNestedPath, g.authConfig.DoubleNestedGroup},
	}

	for _, tier := range pathTiers {
		assertionGroups, err := authactions.GenericSAMLAssertionGroups(g.client, tier.user)
		require.NoError(g.T(), err, "Failed to capture the assertion Keycloak issues for user [%v]", tier.user.Username)

		require.Contains(g.T(), assertionGroups, tier.path, "Keycloak should now assert group [%v] as the path [%v], which is what makes a nested group distinguishable from any other group sharing its name", tier.bareName, tier.path)
		require.NotContains(g.T(), assertionGroups, tier.bareName, "The bare name [%v] should no longer appear, so a binding written against it stops granting anything the moment this setting changes", tier.bareName)
	}

	pathPrincipalID := authactions.GetSAMLGroupPrincipalID(authactions.GenericSAML, nestedPath)

	logrus.Infof("Granting the group at path [%v] the %v role on cluster [%v]", nestedPath, rbac.ClusterOwner, g.cluster.ID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, g.cluster.ID, pathPrincipalID, rbac.ClusterOwner.String())
	require.NoError(g.T(), err, "Failed to create cluster role template binding for principal [%v]", pathPrincipalID)

	for _, userInfo := range g.authConfig.NestedUsers {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.GenericSAML)
		require.NoError(g.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(g.T(), g.client, userClient, g.cluster.ID, rbac.ClusterOwner)
	}

	err = authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	require.NoError(g.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakNameIDFormatDefaultUnspecified() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	logrus.Info("Reconfiguring generic SAML with nameIDFormat left unset")
	err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.NameIDFormat = ""
	})
	require.NoError(g.T(), err, "Generic SAML should enable with nameIDFormat left unset, which the SAML engine defaults to unspecified")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "nameIDFormat left unset", true)
	require.NoError(g.T(), err, "Login should succeed with the default nameIDFormat")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakNameIDFormatVariants() {
	nameIDFormats := []string{"emailAddress", "transient", "persistent"}

	for _, nameIDFormat := range nameIDFormats {
		subSession := g.session.NewSession()
		defer subSession.Cleanup()

		subSession.RegisterCleanupFunc(func() error {
			return authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
				config.NameIDFormat = ""
			})
		})

		logrus.Infof("Reconfiguring generic SAML with nameIDFormat=%s", nameIDFormat)
		err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
			config.NameIDFormat = nameIDFormat
		})
		require.NoError(g.T(), err, "Generic SAML should enable with nameIDFormat=%s; a rejected value would fail the admin login that enabling runs through", nameIDFormat)

		err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "nameIDFormat="+nameIDFormat, true)
		require.NoError(g.T(), err, "Login should succeed with nameIDFormat=%s", nameIDFormat)
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakNameIDFormatInvalidValueRejected() {
	configuredNameIDFormat := g.client.Auth.GenericSAML.Config.NameIDFormat
	g.client.Auth.GenericSAML.Config.NameIDFormat = "bogus"

	defer func() {
		g.client.Auth.GenericSAML.Config.NameIDFormat = configuredNameIDFormat
	}()

	logrus.Info("Enabling generic SAML with an unrecognised nameIDFormat")
	err := g.client.Auth.GenericSAML.Enable()
	require.Error(g.T(), err, "An unrecognised nameIDFormat must be rejected before it ever reaches the SAML engine")
	require.Contains(g.T(), err.Error(), authactions.InvalidOptionError, "Enable should be rejected because nameIDFormat carries a value outside its enum")
	require.Contains(g.T(), err.Error(), authactions.NameIDFormatFieldError, "The rejected field should be nameIDFormat rather than anything else the enable request carries")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakSignatureMethodDefaultRSASHA256() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	logrus.Info("Reconfiguring generic SAML with signatureMethod left unset")
	err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.SignatureMethod = ""
	})
	require.NoError(g.T(), err, "Generic SAML should enable with signatureMethod left unset, which the SAML engine defaults to RSA-SHA256")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "signatureMethod left unset", true)
	require.NoError(g.T(), err, "Login should succeed with the default signatureMethod")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakSignatureMethodVariants() {
	signatureMethods := []string{"RSA-SHA1", "RSA-SHA512"}

	for _, signatureMethod := range signatureMethods {
		subSession := g.session.NewSession()
		defer subSession.Cleanup()

		subSession.RegisterCleanupFunc(func() error {
			return authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
				config.SignatureMethod = ""
			})
		})

		logrus.Infof("Reconfiguring generic SAML with signatureMethod=%s", signatureMethod)
		err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
			config.SignatureMethod = signatureMethod
		})
		require.NoError(g.T(), err, "Generic SAML should enable with signatureMethod=%s; a rejected value would fail the admin login that enabling runs through", signatureMethod)

		err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "signatureMethod="+signatureMethod, true)
		require.NoError(g.T(), err, "Login should succeed with signatureMethod=%s", signatureMethod)
	}
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakSignatureMethodInvalidValueRejected() {
	configuredSignatureMethod := g.client.Auth.GenericSAML.Config.SignatureMethod
	g.client.Auth.GenericSAML.Config.SignatureMethod = "RSA-MD5"

	defer func() {
		g.client.Auth.GenericSAML.Config.SignatureMethod = configuredSignatureMethod
	}()

	logrus.Info("Enabling generic SAML with an unrecognised signatureMethod")
	err := g.client.Auth.GenericSAML.Enable()
	require.Error(g.T(), err, "An unrecognised signatureMethod must be rejected before it ever reaches the SAML engine")
	require.Contains(g.T(), err.Error(), authactions.InvalidOptionError, "Enable should be rejected because signatureMethod carries a value outside its enum")
	require.Contains(g.T(), err.Error(), authactions.SignatureMethodFieldError, "The rejected field should be signatureMethod rather than anything else the enable request carries")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakAllowIdpInitiatedDefaultFalse() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	logrus.Info("Reconfiguring generic SAML with allowIdpInitiated left at its default of false")
	err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.AllowIdpInitiated = false
	})
	require.NoError(g.T(), err, "Generic SAML should enable with allowIdpInitiated left false")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "allowIdpInitiated=false", true)
	require.NoError(g.T(), err, "SP-initiated login should succeed regardless of allowIdpInitiated, since the flag only governs unsolicited assertions")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakAllowIdpInitiatedToggleTrueAndBack() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
			config.AllowIdpInitiated = false
		})
	})

	logrus.Info("Reconfiguring generic SAML with allowIdpInitiated=true")
	err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.AllowIdpInitiated = true
	})
	require.NoError(g.T(), err, "Generic SAML should enable with allowIdpInitiated=true")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "allowIdpInitiated=true", true)
	require.NoError(g.T(), err, "SP-initiated login should still succeed once IdP-initiated assertions are also allowed")

	logrus.Info("Toggling allowIdpInitiated back to false")
	err = authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.AllowIdpInitiated = false
	})
	require.NoError(g.T(), err, "Generic SAML should re-enable with allowIdpInitiated toggled back to false")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "allowIdpInitiated toggled back to false", true)
	require.NoError(g.T(), err, "SP-initiated login should be unaffected by toggling allowIdpInitiated back off")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakForceAuthnDefaultOff() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(g.client, authactions.GenericSAML)
	})

	logrus.Info("Reconfiguring generic SAML with forceAuthn left unset")
	err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.ForceAuthn = nil
	})
	require.NoError(g.T(), err, "Generic SAML should enable with forceAuthn left unset")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "forceAuthn left unset", true)
	require.NoError(g.T(), err, "Login should succeed with the default forceAuthn")
}

func (g *GenericSAMLKeycloakAuthProviderSuite) TestGenericSAMLKeycloakForceAuthnTrueForcesReauthentication() {
	subSession := g.session.NewSession()
	defer subSession.Cleanup()

	forceAuthn := true

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
			config.ForceAuthn = nil
		})
	})

	logrus.Info("Reconfiguring generic SAML with forceAuthn=true")
	err := authactions.ReconfigureGenericSAML(g.client, func(config *saml.Config) {
		config.ForceAuthn = &forceAuthn
	})
	require.NoError(g.T(), err, "Generic SAML should enable with forceAuthn=true; a broken ForceAuthn AuthnRequest would fail the admin login that enabling runs through")

	err = authactions.VerifyUserLogins(g.client, authactions.GenericSAML, []authactions.User{g.authConfig.Users[0]}, "forceAuthn=true", true)
	require.NoError(g.T(), err, "Login should still succeed when the AuthnRequest forces re-authentication at the identity provider")
}

func TestGenericSAMLKeycloakAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(GenericSAMLKeycloakAuthProviderSuite))
}

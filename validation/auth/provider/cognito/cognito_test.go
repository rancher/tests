//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package cognito

import (
	"fmt"
	"net/url"
	"slices"
	"strings"
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth/oidcprovider"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	authconfigsapi "github.com/rancher/tests/actions/kubeapi/authconfigs"
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

type CognitoAuthProviderSuite struct {
	suite.Suite
	session          *session.Session
	cognitoSession   *session.Session
	client           *rancher.Client
	cluster          *v3.Cluster
	adminUser        *v3.User
	adminPrincipalID string
	authConfig       *authactions.ExternalAuthConfig
}

func (c *CognitoAuthProviderSuite) SetupSuite() {
	c.session = session.NewSession()
	c.cognitoSession = session.NewSession()

	setupComplete := false
	defer func() {
		if !setupComplete {
			c.session.Cleanup()
			c.cognitoSession.Cleanup()
		}
	}()

	client, err := rancher.NewClient("", c.session)
	require.NoError(c.T(), err, "Failed to create Rancher client")
	c.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(c.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(c.client, clusterName)
	require.NoError(c.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	c.cluster, err = c.client.Management.Cluster.ByID(clusterID)
	require.NoError(c.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Connecting to AWS Cognito as a user pool administrator")
	cognitoClient, err := authactions.NewCognitoClient(c.cognitoSession)
	require.NoError(c.T(), err, "Failed to create Cognito admin client")

	logrus.Info("Building the Cognito user pool and settling the test accounts it holds")
	admin, authInput, err := authactions.SetupCognito(c.client, cognitoClient)
	require.NoError(c.T(), err, "Failed to set up the Cognito user pool")

	c.authConfig = authInput
	require.NotEmpty(c.T(), c.authConfig.Group, "Cognito setup should have settled on the allowed group")
	require.NotEmpty(c.T(), c.authConfig.Users, "Cognito setup should have settled on the users to sign in as")
	require.NotEmpty(c.T(), c.authConfig.ExcludedUsers, "Cognito setup should have settled on a user outside the allowed group")

	c.adminUser = &v3.User{
		Username: admin.Username,
		Password: admin.Password,
	}
	c.adminPrincipalID = authactions.GetExternalUserPrincipalID(authactions.Cognito, admin)

	setupComplete = true
}

func (c *CognitoAuthProviderSuite) TearDownSuite() {
	defer c.cognitoSession.Cleanup()
	defer c.session.Cleanup()

	if c.client != nil {
		enabled, err := authconfigsapi.AuthConfigEnabled(c.client, authactions.Cognito)
		require.NoError(c.T(), err, "Failed to read whether Cognito is enabled in teardown")

		if enabled {
			logrus.Info("Disabling Cognito authentication after test suite")
			err := c.client.Auth.Cognito.Disable()
			require.NoError(c.T(), err, "Failed to disable Cognito in teardown")
		}
	}
}

func (c *CognitoAuthProviderSuite) TestCognitoEnableProvider() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to enable Cognito")

	cognitoConfig, err := c.client.Management.AuthConfig.ByID(authactions.Cognito)
	require.NoError(c.T(), err, "Failed to retrieve Cognito config")

	require.True(c.T(), cognitoConfig.Enabled, "Cognito should be enabled")
	require.Equal(c.T(), oidcprovider.Cognito.ConfigType, cognitoConfig.Type, "Auth config should be stored as the Cognito subtype")
	require.Equal(c.T(), authactions.AuthProvCleanupAnnotationValUnlocked, cognitoConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be unlocked")
}

func (c *CognitoAuthProviderSuite) TestCognitoDisableAndReenableProvider() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to enable Cognito")

	err = c.client.Auth.Cognito.Disable()
	require.NoError(c.T(), err, "Failed to disable Cognito")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	})

	cognitoConfig, err := authactions.WaitForAuthProviderAnnotationUpdate(c.client, authactions.Cognito, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(c.T(), err, "Failed waiting for annotation update")

	require.False(c.T(), cognitoConfig.Enabled, "Cognito should be disabled")
	require.Equal(c.T(), authactions.AuthProvCleanupAnnotationValLocked, cognitoConfig.Annotations[authactions.AuthProvCleanupAnnotationKey], "Annotation should be locked")

	err = authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to re-enable Cognito")
}

func (c *CognitoAuthProviderSuite) TestCognitoConfigureTestReturnsAuthorizationRequest() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to enable Cognito")

	cognitoConfig := c.client.Auth.Cognito.Config

	redirectURL, err := c.client.Auth.Cognito.ConfigureTest()
	require.NoError(c.T(), err, "Rancher should answer configureTest with the address the administrator signs in at")

	authorizationRequest, err := url.Parse(redirectURL)
	require.NoError(c.T(), err, "configureTest should answer with a parseable address, got [%v]", redirectURL)

	require.True(c.T(), strings.HasPrefix(redirectURL, cognitoConfig.AuthEndpoint),
		"configureTest should send the administrator to the hosted UI authorization endpoint [%v], got [%v]",
		cognitoConfig.AuthEndpoint, redirectURL)

	parameters := authorizationRequest.Query()
	require.Equal(c.T(), cognitoConfig.ClientID, parameters.Get("client_id"), "The authorization request should name the registered app client")
	require.Equal(c.T(), "code", parameters.Get("response_type"), "The authorization request should ask for an authorization code")
	require.Equal(c.T(), cognitoConfig.RancherURL, parameters.Get("redirect_uri"), "The authorization request should come back to the configured Rancher address")

	atLeast216, err := actionsClusters.IsRancherVersionAbove(c.client, "v2.16.0-0")
	require.NoError(c.T(), err, "Failed to read the Rancher server version")
	if !atLeast216 {
		c.T().Skip("Known issue: https://github.com/rancher/rancher/issues/57708")
	}

	require.Contains(c.T(), strings.Fields(parameters.Get("scope")), "openid",
		"The authorization request should ask for the openid scope so the code it yields comes with an id_token, got scope [%v]",
		parameters.Get("scope"))
}

func (c *CognitoAuthProviderSuite) TestCognitoAdminLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	authenticatedUser, err := authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(c.T(), err, "Failed to retrieve the user the OpenID Connect session authenticated as")
	require.Contains(c.T(), authenticatedUser.PrincipalIDs, c.adminPrincipalID, "Session should resolve to the Cognito principal")

	var localPrincipals []string
	for _, principalID := range authenticatedUser.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.LocalPrincipalPrefix) {
			localPrincipals = append(localPrincipals, principalID)
		}
	}
	require.NotEmpty(c.T(), localPrincipals, "Enabling should have attached the Cognito identity to the existing Rancher administrator, so the same user should still hold its local principal")
}

func (c *CognitoAuthProviderSuite) TestCognitoGroupsClaimCarriesMembership() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to enable Cognito")

	member := c.authConfig.Users[0]

	claimGroups, err := authactions.CognitoClaimValues(c.client, member, authactions.CognitoGroupsClaim)
	require.NoError(c.T(), err, "Failed to read the claims Cognito issues for [%v]", member.Username)

	require.Contains(c.T(), claimGroups, c.authConfig.Group,
		"Cognito should name the group [%v] in the %v claim of the token it issues for [%v], which is where Rancher reads membership from",
		c.authConfig.Group, authactions.CognitoGroupsClaim, member.Username)
}

func (c *CognitoAuthProviderSuite) TestCognitoUnrestrictedAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeUnrestricted, nil)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeUnrestricted, newAuthConfig.AccessMode, "Access mode should be unrestricted")

	allUsers := slices.Concat(c.authConfig.Users, c.authConfig.ExcludedUsers)
	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, allUsers, authactions.AccessModeUnrestricted+" access mode", true)
	require.NoError(c.T(), err, "Every Cognito user should be able to login")
}

func (c *CognitoAuthProviderSuite) TestCognitoRestrictedAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, c.cluster.ID, c.authConfig, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, c.adminPrincipalID)

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRestricted, principalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.Users, authactions.AccessModeRestricted+" access mode", true)
	require.NoError(c.T(), err, "Members of the allowed group should be able to login")
}

func (c *CognitoAuthProviderSuite) TestCognitoRestrictedAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(c.T(), c.authConfig.ExcludedUsers, "Cognito auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, c.cluster.ID, c.authConfig, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup restricted access mode test")

	principalIDs = append(principalIDs, c.adminPrincipalID)

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRestricted, principalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.ExcludedUsers, authactions.AccessModeRestricted+" access mode", false)
	require.NoError(c.T(), err, "Users outside the allowed group should NOT be able to login")
}

func (c *CognitoAuthProviderSuite) TestCognitoRequiredAccessModeAuthorizedUsersCanLogin() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, c.cluster.ID, c.authConfig, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, c.adminPrincipalID)

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRequired, principalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.Users, authactions.AccessModeRequired+" access mode", true)
	require.NoError(c.T(), err, "Members of the allowed group should be able to login")
}

func (c *CognitoAuthProviderSuite) TestCognitoRequiredAccessModeUnauthorizedLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(c.T(), c.authConfig.ExcludedUsers, "Cognito auth input must list users outside the allowed group to prove they are turned away")

	principalIDs, err := authactions.SetupExternalRequiredAccessModePrincipals(authAdmin, c.cluster.ID, c.authConfig, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup required access mode test")

	principalIDs = append(principalIDs, c.adminPrincipalID)

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRequired, principalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRequired, newAuthConfig.AccessMode, "Access mode should be required")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.ExcludedUsers, authactions.AccessModeRequired+" access mode", false)
	require.NoError(c.T(), err, "Users outside the allowed group should NOT be able to login")
}

func (c *CognitoAuthProviderSuite) TestCognitoGroupClusterAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group)

	logrus.Infof("Granting Cognito group [%v] the %v role on cluster [%v]", c.authConfig.Group, rbac.ClusterOwner, c.cluster.ID)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, c.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(c.T(), err, "Failed to create cluster role template binding")

	for _, userInfo := range c.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.Cognito)
		require.NoError(c.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(c.T(), c.client, userClient, c.cluster.ID, rbac.ClusterOwner)
	}

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(c.client, groupPrincipalID, c.cluster.ID)
	require.NoError(c.T(), err, "Failed to get group CRTB")
	require.NotNil(c.T(), foundCRTB, "Cluster role binding should exist for group")

}

func (c *CognitoAuthProviderSuite) TestCognitoGroupProjectAccess() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, c.cluster.ID)
	require.NoError(c.T(), err, "Failed to create project and namespace")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group)

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	logrus.Infof("Granting Cognito group [%v] the %v role on project [%v]", c.authConfig.Group, rbac.ProjectOwner, projectName)
	groupPRTBResp, err := rbacapi.CreateGroupProjectRoleTemplateBinding(authAdmin, projectName, prtbNamespace, groupPrincipalID, rbac.ProjectOwner.String())
	require.NoError(c.T(), err, "Failed to create PRTB")
	require.NotNil(c.T(), groupPRTBResp, "PRTB should be created")

	for _, userInfo := range c.authConfig.Users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}
		userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.Cognito)
		require.NoError(c.T(), err, "Failed to login user [%v]", userInfo.Username)

		_, err = userClient.WranglerContext.Mgmt.Project().Get(projectResp.Namespace, projectResp.Name, metav1.GetOptions{})
		require.NoError(c.T(), err, "User [%v] should be able to get project %s because the token places them in group [%v]", userInfo.Username, projectResp.Name, c.authConfig.Group)
	}

}

func (c *CognitoAuthProviderSuite) TestCognitoNonMemberClusterAccessDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(c.T(), c.authConfig.ExcludedUsers, "Cognito auth input must list a user outside the allowed group")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group)

	logrus.Infof("Granting Cognito group [%v] the %v role on cluster [%v]", c.authConfig.Group, rbac.ClusterOwner, c.cluster.ID)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, c.cluster.ID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(c.T(), err, "Failed to create cluster role template binding")

	outsider := c.authConfig.ExcludedUsers[0]
	user := &v3.User{
		Username: outsider.Username,
		Password: outsider.Password,
	}

	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to login user [%v]", outsider.Username)

	_, err = userClient.WranglerContext.Mgmt.Cluster().Get(c.cluster.ID, metav1.GetOptions{})
	require.True(c.T(), apierrors.IsForbidden(err), "User [%v] is outside group [%v], so the cluster the group owns should be forbidden, got: %v", outsider.Username, c.authConfig.Group, err)

}

func (c *CognitoAuthProviderSuite) TestCognitoEnableIntoRestrictedAccessModeIsNotGuarded() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to enable Cognito")

	configuredAccessMode := c.client.Auth.Cognito.Config.AccessMode
	configuredPrincipalIDs := slices.Clone(c.client.Auth.Cognito.Config.AllowedPrincipalIDs)
	subSession.RegisterCleanupFunc(func() error {
		c.client.Auth.Cognito.Config.AccessMode = configuredAccessMode
		c.client.Auth.Cognito.Config.AllowedPrincipalIDs = configuredPrincipalIDs

		if err := c.client.Auth.Cognito.Disable(); err != nil {
			return err
		}

		if _, err := authactions.WaitForAuthProviderAnnotationUpdate(c.client, authactions.Cognito, authactions.AuthProvCleanupAnnotationValLocked); err != nil {
			return err
		}

		if err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito); err != nil {
			return err
		}

		_, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, configuredAccessMode, configuredPrincipalIDs)

		return err
	})

	logrus.Info("Disabling Cognito so the enable path runs against a fresh provider")
	err = c.client.Auth.Cognito.Disable()
	require.NoError(c.T(), err, "Failed to disable Cognito")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(c.client, authactions.Cognito, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(c.T(), err, "Failed waiting for annotation update")

	c.client.Auth.Cognito.Config.AccessMode = authactions.AccessModeRestricted
	c.client.Auth.Cognito.Config.AllowedPrincipalIDs = nil

	logrus.Info("Enabling Cognito by writing the auth config directly, into restricted access mode with an empty allow list")
	err = c.client.Auth.Cognito.Enable()
	require.NoError(c.T(), err, "Writing the auth config runs no login for Rancher to check access against, so nothing stops the provider coming up restricted to an empty allow list")

	cognitoConfig, err := c.client.Management.AuthConfig.ByID(authactions.Cognito)
	require.NoError(c.T(), err, "Failed to retrieve Cognito config")
	require.True(c.T(), cognitoConfig.Enabled, "Cognito should be enabled after an accepted config write")
	require.Equal(c.T(), authactions.AccessModeRestricted, cognitoConfig.AccessMode, "The provider should have come up in the access mode it was enabled with")
	require.NotContains(c.T(), cognitoConfig.AllowedPrincipalIDs, c.adminPrincipalID, "Writing the config adds no principal of its own, so the administrator who enabled the provider is named nowhere in the allow list that now governs it")
}

func (c *CognitoAuthProviderSuite) TestCognitoEnableWithAdminLoginChecksAccessMode() {
	subSession := c.session.NewSession()
	defer subSession.Cleanup()

	err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to enable Cognito")

	configuredAccessMode := c.client.Auth.Cognito.Config.AccessMode
	configuredPrincipalIDs := slices.Clone(c.client.Auth.Cognito.Config.AllowedPrincipalIDs)
	subSession.RegisterCleanupFunc(func() error {
		c.client.Auth.Cognito.Config.AccessMode = configuredAccessMode
		c.client.Auth.Cognito.Config.AllowedPrincipalIDs = configuredPrincipalIDs

		if err := authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito); err != nil {
			return err
		}

		_, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, configuredAccessMode, configuredPrincipalIDs)

		return err
	})

	logrus.Info("Disabling Cognito so the enable path runs against a fresh provider")
	err = c.client.Auth.Cognito.Disable()
	require.NoError(c.T(), err, "Failed to disable Cognito")

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(c.client, authactions.Cognito, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(c.T(), err, "Failed waiting for annotation update")

	c.client.Auth.Cognito.Config.AccessMode = authactions.AccessModeRestricted
	c.client.Auth.Cognito.Config.AllowedPrincipalIDs = nil

	logrus.Info("Enabling Cognito through an administrator sign-in, into restricted access mode with an empty allow list")
	err = c.client.Auth.Cognito.EnableWithAdminLogin(c.adminUser.Username, c.adminUser.Password)
	require.Error(c.T(), err, "Enabling through a sign-in resolves the administrator against the allow list before the config is saved, so an empty allow list must lock the administrator out rather than let them enable a provider they could not then use")

	cognitoConfig, err := c.client.Management.AuthConfig.ByID(authactions.Cognito)
	require.NoError(c.T(), err, "Failed to retrieve Cognito config")
	require.False(c.T(), cognitoConfig.Enabled, "Cognito should remain disabled after a rejected sign-in")
}

func (c *CognitoAuthProviderSuite) TestCognitoRestrictedModeBindings() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, c.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(c.T(), err, "Failed to create cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, c.cluster.ID)
	require.NoError(c.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(c.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	for _, userInfo := range c.authConfig.Users {
		userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, userInfo)
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
		require.NoError(c.T(), err, "Failed to create PRTB for user [%v]", userInfo.Username)
		require.NotNil(c.T(), userPRTBResp, "PRTB should be created for user [%v]", userInfo.Username)
	}

	allowedPrincipalIDs := []string{groupPrincipalID, c.adminPrincipalID}

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	logrus.Infof("Restricting Cognito to group [%v] and the administrator", c.authConfig.Group)
	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.Users, "restricted access mode holding cluster and project bindings", true)
	require.NoError(c.T(), err, "Members bound to the cluster and to the project should still be able to login once the provider is restricted to their group")
}

func (c *CognitoAuthProviderSuite) TestCognitoAllowClusterAndProjectMembersAccessMode() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NotEmpty(c.T(), c.authConfig.ExcludedUsers, "Cognito auth input must list a user outside the allowed group, since this test admits one on a project binding alone")

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, c.cluster.ID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(c.T(), err, "Failed to create group cluster role template binding")

	projectResp, _, err := projectapi.CreateProjectAndNamespace(authAdmin, c.cluster.ID)
	require.NoError(c.T(), err, "Failed to create project")

	prtbNamespace := projectResp.Name
	if projectResp.Status.BackingNamespace != "" {
		prtbNamespace = projectResp.Status.BackingNamespace
	}

	err = authactions.WaitForNamespaceReady(authAdmin, prtbNamespace)
	require.NoError(c.T(), err, "Namespace should be ready")

	projectName := fmt.Sprintf("%s:%s", projectResp.Namespace, projectResp.Name)

	outsider := c.authConfig.ExcludedUsers[0]
	outsiderPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, outsider)

	logrus.Infof("Signing user [%v] in while access is still unrestricted, so that a Rancher user record exists for them", outsider.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, []authactions.User{outsider}, "unrestricted access mode", true)
	require.NoError(c.T(), err, "User [%v] should be able to login before the provider is restricted; restricted access mode reads a user's bindings off their Rancher user record, which only a login creates", outsider.Username)

	_, err = userapi.WaitForUserByPrincipalID(c.client, outsiderPrincipalID)
	require.NoError(c.T(), err, "Login should have created a Rancher user record carrying principal [%v]", outsiderPrincipalID)

	logrus.Infof("Granting user [%v], who is outside group [%v], the %v role on project [%v]", outsider.Username, c.authConfig.Group, rbac.ProjectOwner, projectName)
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
	require.NoError(c.T(), err, "Failed to create PRTB for user [%v]", outsider.Username)
	require.NotNil(c.T(), outsiderPRTBResp, "PRTB should be created for user [%v]", outsider.Username)

	allowedPrincipalIDs := []string{groupPrincipalID, c.adminPrincipalID}

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	logrus.Infof("Restricting Cognito to group [%v] and the administrator, leaving user [%v] listed nowhere", c.authConfig.Group, outsider.Username)
	newAuthConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRestricted, allowedPrincipalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRestricted, newAuthConfig.AccessMode, "Access mode should be restricted")
	require.ElementsMatch(c.T(), allowedPrincipalIDs, newAuthConfig.AllowedPrincipalIDs, "Allowed principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.Users, "restricted access mode as members of an allowed group", true)
	require.NoError(c.T(), err, "Members of the allowed group should be able to login")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, []authactions.User{outsider}, "restricted access mode as a project member only", true)
	require.NoError(c.T(), err, "Restricted access mode should admit user [%v] on their project binding alone, even though no principal of theirs is in the allow list", outsider.Username)
}

func (c *CognitoAuthProviderSuite) TestCognitoRequiredModeRevokedPrincipalLoginDenied() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	grantedPrincipalIDs := []string{
		authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group),
		c.adminPrincipalID,
	}
	for _, user := range c.authConfig.Users {
		grantedPrincipalIDs = append(grantedPrincipalIDs, authactions.GetExternalUserPrincipalID(authactions.Cognito, user))
	}

	restoreAccessMode, err := authactions.CaptureAccessModeRestore(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to record the Cognito access mode")
	subSession.RegisterCleanupFunc(restoreAccessMode)

	logrus.Infof("Granting Cognito group [%v] and its members access in required mode", c.authConfig.Group)
	grantedConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRequired, grantedPrincipalIDs)
	require.NoError(c.T(), err, "Failed to update access mode")
	require.Equal(c.T(), authactions.AccessModeRequired, grantedConfig.AccessMode, "Access mode should be required")
	require.ElementsMatch(c.T(), grantedPrincipalIDs, grantedConfig.AllowedPrincipalIDs, "Granted principals should be persisted exactly as sent")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.Users, "required access mode with principals granted", true)
	require.NoError(c.T(), err, "Granted users should be able to login")

	revokedPrincipalIDs := []string{c.adminPrincipalID}

	logrus.Infof("Revoking Cognito group [%v] and its members, leaving only the administrator allowed", c.authConfig.Group)
	revokedConfig, err := authactions.UpdateAccessMode(c.client, authactions.Cognito, authactions.AccessModeRequired, revokedPrincipalIDs)
	require.NoError(c.T(), err, "Failed to revoke principals")
	require.Equal(c.T(), authactions.AccessModeRequired, revokedConfig.AccessMode, "Access mode should remain required")
	require.ElementsMatch(c.T(), revokedPrincipalIDs, revokedConfig.AllowedPrincipalIDs, "Revoked principals should no longer appear in the allow list")

	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, c.authConfig.Users, "required access mode after principals revoked", false)
	require.NoError(c.T(), err, "Revoked users should NOT be able to login")

	stillAllowed := []authactions.User{{Username: c.adminUser.Username, Password: c.adminUser.Password}}
	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, stillAllowed, "required access mode after principals revoked", true)
	require.NoError(c.T(), err, "The administrator, whose principal was left in the allow list, should still be able to login")
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchFindsGroups() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := c.authConfig.Group
	logrus.Infof("Searching principals for Cognito group [%v]", groupName)
	expectedPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedPrincipalID)
	require.NoError(c.T(), err, "Group [%v] should be returned by principal search", groupName)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchFindsUsers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	allUsers := slices.Concat(c.authConfig.Users, c.authConfig.ExcludedUsers)
	for _, userInfo := range allUsers {
		principalName := authactions.PrincipalNameOf(userInfo)
		logrus.Infof("Searching principals for Cognito user [%v] by the name their principal is built from, [%v]", userInfo.Username, principalName)
		expectedPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, userInfo)

		err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, principalName, expectedPrincipalID)
		require.NoError(c.T(), err, "User [%v] should be returned by principal search", userInfo.Username)
	}
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchByPrincipalType() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := c.authConfig.Group
	expectedGroupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, groupName)

	logrus.Infof("Searching principals for Cognito group [%v] restricted to type [%v]", groupName, authactions.PrincipalTypeGroup)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, groupName, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(c.T(), err, "Search restricted to groups should return group [%v] and nothing of another type, even though an unrestricted search returns the same name as a user as well", groupName)

	userInfo := c.authConfig.Users[0]
	principalName := authactions.PrincipalNameOf(userInfo)
	expectedUserPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, userInfo)

	logrus.Infof("Searching principals for Cognito user [%v] restricted to type [%v]", userInfo.Username, authactions.PrincipalTypeUser)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, principalName, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(c.T(), err, "Search restricted to users should return user [%v] and nothing of another type", userInfo.Username)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchByPartialName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := c.authConfig.Group
	require.NotEmpty(c.T(), groupName, "Group name should be set in the auth configuration")
	groupPrefix := groupName[:len(groupName)/2+1]

	logrus.Infof("Searching principals for Cognito group [%v] using partial name [%v]", groupName, groupPrefix)
	prefixPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, groupPrefix)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, prefixPrincipalID)
	require.NoError(c.T(), err, "A Cognito principal search returns the term it was given, so partial name [%v] should come back as a principal in its own right", groupPrefix)

	fullPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, groupName)

	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupPrefix, fullPrincipalID)
	require.Error(c.T(), err, "Cognito exposes no directory to search, so partial name [%v] must not resolve to group [%v]; anyone treating this search as a lookup would bind principal [%v], which matches nobody", groupPrefix, groupName, prefixPrincipalID)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchEchoesUnknownNames() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	unknownName := "cognito-no-such-principal"
	expectedPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, authactions.User{Username: unknownName})

	logrus.Infof("Searching principals for [%v], which exists nowhere in the Cognito user pool", unknownName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, unknownName, expectedPrincipalID)
	require.NoError(c.T(), err, "A Cognito principal search reaches no directory and answers from the term alone, so [%v] comes back as a principal despite matching no account; a search result is therefore not evidence that a principal exists", unknownName)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalByIDResolvesGroupAndUser() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, c.authConfig.Group)

	logrus.Infof("Resolving Cognito group principal [%v] by ID", groupPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, groupPrincipalID, authactions.Cognito, authactions.PrincipalTypeGroup)
	require.NoError(c.T(), err, "Group principal [%v] should resolve by ID", groupPrincipalID)

	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, c.authConfig.Users[0])

	logrus.Infof("Resolving Cognito user principal [%v] by ID", userPrincipalID)
	err = authactions.VerifyPrincipalByID(authAdmin, userPrincipalID, authactions.Cognito, authactions.PrincipalTypeUser)
	require.NoError(c.T(), err, "User principal [%v] should resolve by ID", userPrincipalID)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchFindsLocalUserByDisplayName() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	logrus.Info("Creating a local user whose display name differs from its username")
	localUser, err := userapi.CreateUser(authAdmin)
	require.NoError(c.T(), err, "Failed to create local user")
	require.NotEqual(c.T(), localUser.Username, localUser.DisplayName, "Local user display name must differ from its username for this search to be meaningful")

	logrus.Infof("Searching principals for local user by username [%v]", localUser.Username)
	err = authactions.VerifyPrincipalIsLocal(c.client, localUser.Username)
	require.NoError(c.T(), err, "Local user [%v] should be findable by username while Cognito is enabled", localUser.Username)

	logrus.Infof("Searching principals for local user by display name [%v]", localUser.DisplayName)
	err = authactions.VerifyPrincipalIsLocal(c.client, localUser.DisplayName)
	require.NoError(c.T(), err, "Local user [%v] should be findable by display name [%v]", localUser.Username, localUser.DisplayName)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchProvisionedUserIsNotLocal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userInfo := c.authConfig.Users[0]
	logrus.Infof("Logging in as Cognito user [%v] to provision the Rancher user", userInfo.Username)
	user := &v3.User{
		Username: userInfo.Username,
		Password: userInfo.Password,
	}
	userClient, err := authactions.LoginAsAuthUser(authAdmin, user, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to login user [%v]", userInfo.Username)

	provisionedUser, err := c.client.WranglerContext.Mgmt.User().Get(userClient.UserID, metav1.GetOptions{})
	require.NoError(c.T(), err, "Failed to retrieve the Rancher user provisioned for [%v]", userInfo.Username)
	logrus.Infof("Cognito user [%v] provisioned Rancher user [%v] with display name [%v], username [%v] and principals %v",
		userInfo.Username, provisionedUser.Name, provisionedUser.DisplayName, provisionedUser.Username, provisionedUser.PrincipalIDs)

	require.Empty(c.T(), provisionedUser.Username, "Externally provisioned user [%v] should carry no local login username", userInfo.Username)
	require.Greater(c.T(), len(provisionedUser.PrincipalIDs), 1, "Externally provisioned user [%v] should carry an external principal alongside the local one", userInfo.Username)

	expectedPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, userInfo)

	logrus.Infof("Searching principals for provisioned user [%v]", userInfo.Username)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, userInfo.Username, expectedPrincipalID)
	require.NoError(c.T(), err, "Provisioned user [%v] should stay findable in the user pool", userInfo.Username)

	err = authactions.VerifyPrincipalNotLocal(authAdmin, userInfo.Username)
	require.NoError(c.T(), err, "Provisioned user [%v] should not surface as a local principal", userInfo.Username)
}

func (c *CognitoAuthProviderSuite) TestCognitoPrincipalSearchAfterProviderDisabled() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupName := c.authConfig.Group
	expectedGroupPrincipalID := authactions.GetExternalGroupPrincipalID(authactions.Cognito, groupName)

	logrus.Infof("Confirming Cognito group [%v] is returned while the provider is enabled", groupName)
	err = authactions.VerifyPrincipalSearchReturnsID(authAdmin, groupName, expectedGroupPrincipalID)
	require.NoError(c.T(), err, "Group [%v] should be returned while Cognito is enabled", groupName)

	logrus.Info("Disabling Cognito before searching principals again")
	err = c.client.Auth.Cognito.Disable()
	require.NoError(c.T(), err, "Failed to disable Cognito")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(c.client, authactions.Cognito, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(c.T(), err, "Failed waiting for annotation update")

	logrus.Info("Waiting for the OpenID Connect session that found the group to stop being accepted")
	err = authactions.VerifyProviderSessionRejected(authAdmin)
	require.NoError(c.T(), err, "Disabling the provider should delete the tokens it issued, so the session that reached the Cognito principals should stop working")

	logrus.Infof("Searching principals for [%v] with Cognito disabled", groupName)
	err = authactions.VerifyPrincipalSearchExcludesProvider(c.client, groupName, authactions.Cognito)
	require.NoError(c.T(), err, "No Cognito principal should be returned while the provider is disabled")
}

func (c *CognitoAuthProviderSuite) TestCognitoLoginAttachesExternalPrincipal() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	cognitoUser := c.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, cognitoUser)

	logrus.Infof("Logging in as Cognito user %s so that the login flow attaches its external principal", cognitoUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, []authactions.User{cognitoUser}, "external principal attachment", true)
	require.NoError(c.T(), err, "Cognito user should be able to login")

	logrus.Infof("Verifying that a user record carrying the external principal %s exists", userPrincipalID)
	attachedUser, err := userapi.WaitForUserByPrincipalID(c.client, userPrincipalID)
	require.NoError(c.T(), err, "Login should attach the external principal to a user record")
	require.Contains(c.T(), attachedUser.PrincipalIDs, userPrincipalID, "User record should carry the external principal")
	require.Contains(c.T(), attachedUser.PrincipalIDs, authactions.LocalPrincipalPrefix+attachedUser.Name, "User record should also carry its local principal")
}

func (c *CognitoAuthProviderSuite) TestCognitoDisableRemovesExternalPrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	cognitoUser := c.authConfig.Users[0]
	userPrincipalID := authactions.GetExternalUserPrincipalID(authactions.Cognito, cognitoUser)

	logrus.Infof("Logging in as Cognito user %s so that a user record carrying its external principal exists", cognitoUser.Username)
	err = authactions.VerifyUserLogins(authAdmin, authactions.Cognito, []authactions.User{cognitoUser}, "external principal attachment", true)
	require.NoError(c.T(), err, "Cognito user should be able to login")

	attachedUser, err := userapi.WaitForUserByPrincipalID(c.client, userPrincipalID)
	require.NoError(c.T(), err, "Login should attach the external principal to a user record")
	require.ElementsMatch(c.T(), []string{userPrincipalID, authactions.LocalPrincipalPrefix + attachedUser.Name}, attachedUser.PrincipalIDs,
		"User %s should carry only the Cognito principal and the local principal added alongside it, so that disabling the provider leaves it with no external identity", attachedUser.Name)

	logrus.Info("Disabling Cognito so that the cleanup service reconciles principals it emitted")
	err = c.client.Auth.Cognito.Disable()
	require.NoError(c.T(), err, "Failed to disable Cognito")

	subSession.RegisterCleanupFunc(func() error {
		return authactions.EnsureAuthProviderEnabled(c.client, authactions.Cognito)
	})

	_, err = authactions.WaitForAuthProviderAnnotationUpdate(c.client, authactions.Cognito, authactions.AuthProvCleanupAnnotationValLocked)
	require.NoError(c.T(), err, "Failed waiting for annotation update")

	logrus.Infof("Verifying that no user record still carries the external principal %s", userPrincipalID)
	err = userapi.WaitForUserByPrincipalIDDeletion(c.client, userPrincipalID)
	require.NoError(c.T(), err, "Disabling the provider should leave no user carrying a Cognito principal")

	logrus.Infof("Verifying that user %s was removed rather than left behind holding only its local principal", attachedUser.Name)
	err = userapi.WaitForUserDeletion(c.client, attachedUser.Name)
	require.NoError(c.T(), err, "Disabling the provider should remove a user whose only external identity that provider emitted, leaving no record that could later be rebound")
}

func TestCognitoAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(CognitoAuthProviderSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package cognito

import (
	"net/http"
	"net/url"
	"strings"

	authext "github.com/rancher/shepherd/extensions/auth"
	authactions "github.com/rancher/tests/actions/auth"
	authconfigsapi "github.com/rancher/tests/actions/kubeapi/authconfigs"
	"github.com/stretchr/testify/require"
)

func (c *CognitoAuthProviderSuite) TestCognitoLogoutAllOffersTheIdentityProviderRedirect() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	originalEnabled, originalForced, err := authconfigsapi.FindLogoutAllSettings(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to read the single logout settings")

	err = c.client.Auth.Cognito.UpdateLogoutAll(true, false)
	require.NoError(c.T(), err, "Failed to turn single logout on")
	subSession.RegisterCleanupFunc(func() error {
		return c.client.Auth.Cognito.UpdateLogoutAll(originalEnabled, originalForced)
	})

	redirectURL, err := authactions.LogoutAll(authAdmin, authactions.Cognito)
	require.NoError(c.T(), err, "Rancher should answer logout-all with the address that ends the Cognito session")

	logoutRequest, err := url.Parse(redirectURL)
	require.NoError(c.T(), err, "logout-all should answer with a parseable address, got [%v]", redirectURL)

	cognitoConfig := c.client.Auth.Cognito.Config
	require.True(c.T(), strings.HasPrefix(redirectURL, cognitoConfig.EndSessionEndpoint),
		"logout-all should send the browser to the hosted UI logout endpoint [%v], got [%v]",
		cognitoConfig.EndSessionEndpoint, redirectURL)

	require.Equal(c.T(), cognitoConfig.ClientID, logoutRequest.Query().Get("client_id"),
		"Cognito ends the session of the app client named in the logout request")
}

func (c *CognitoAuthProviderSuite) TestCognitoLogoutAllDisabledRefusesTheRedirect() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	originalEnabled, originalForced, err := authconfigsapi.FindLogoutAllSettings(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to read the single logout settings")

	err = c.client.Auth.Cognito.UpdateLogoutAll(false, false)
	require.NoError(c.T(), err, "Failed to turn single logout off")
	subSession.RegisterCleanupFunc(func() error {
		return c.client.Auth.Cognito.UpdateLogoutAll(originalEnabled, originalForced)
	})

	_, err = authactions.LogoutAll(authAdmin, authactions.Cognito)
	require.NoError(c.T(), authactions.VerifyLogoutRefused(err, http.StatusInternalServerError),
		"A provider that is not configured for single logout should refuse logout-all rather than answer with a redirect")

	_, err = authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(c.T(), err, "The refused logout-all should leave the session in place")
}

func (c *CognitoAuthProviderSuite) TestCognitoLogoutAllForcedRejectsAPlainLogout() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	originalEnabled, originalForced, err := authconfigsapi.FindLogoutAllSettings(c.client, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to read the single logout settings")

	err = c.client.Auth.Cognito.UpdateLogoutAll(true, true)
	require.NoError(c.T(), err, "Failed to force single logout")
	subSession.RegisterCleanupFunc(func() error {
		return c.client.Auth.Cognito.UpdateLogoutAll(originalEnabled, originalForced)
	})

	err = authext.Logout(authAdmin)
	require.NoError(c.T(), authactions.VerifyLogoutRefused(err, http.StatusInternalServerError),
		"A provider configured for forced single logout should turn a plain logout away, so the session can only be ended at Cognito")

	_, err = authAdmin.Management.User.ByID(authAdmin.UserID)
	require.NoError(c.T(), err, "The refused plain logout should leave the session in place")
}

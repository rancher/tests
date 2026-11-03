//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package cognito

import (
	"net/url"
	"strings"

	authext "github.com/rancher/shepherd/extensions/auth"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/stretchr/testify/require"
)

func (c *CognitoAuthProviderSuite) TestCognitoLogoutAllOffersTheIdentityProviderRedirect() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	err = c.client.Auth.Cognito.UpdateLogoutAll(true, false)
	require.NoError(c.T(), err, "Failed to turn single logout on")
	subSession.RegisterCleanupFunc(func() error {
		return c.client.Auth.Cognito.UpdateLogoutAll(false, false)
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

	err = c.client.Auth.Cognito.UpdateLogoutAll(false, false)
	require.NoError(c.T(), err, "Failed to turn single logout off")

	_, err = authactions.LogoutAll(authAdmin, authactions.Cognito)
	require.Error(c.T(), err, "A provider that is not configured for single logout should refuse logout-all rather than answer with a redirect")
}

func (c *CognitoAuthProviderSuite) TestCognitoLogoutAllForcedRejectsAPlainLogout() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(c.client, c.session, c.adminUser, authactions.Cognito)
	require.NoError(c.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	err = c.client.Auth.Cognito.UpdateLogoutAll(true, true)
	require.NoError(c.T(), err, "Failed to force single logout")
	subSession.RegisterCleanupFunc(func() error {
		return c.client.Auth.Cognito.UpdateLogoutAll(false, false)
	})

	err = authext.Logout(authAdmin)
	require.Error(c.T(), err, "A provider configured for forced single logout should turn a plain logout away, so the session can only be ended at Cognito")
}

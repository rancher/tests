//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package keycloakoidc

import (
	"net/url"
	"strings"

	authactions "github.com/rancher/tests/actions/auth"
	"github.com/stretchr/testify/require"
)

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCConfigureTestRequestsOpenIDScope() {
	err := authactions.EnsureAuthProviderEnabled(k.client, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to enable Keycloak OIDC")

	redirectURL, err := k.client.Auth.KeycloakOIDC.ConfigureTest()
	require.NoError(k.T(), err, "Rancher should answer configureTest with the address the administrator signs in at")

	authorizationRequest, err := url.Parse(redirectURL)
	require.NoError(k.T(), err, "configureTest should answer with a parseable address, got [%v]", redirectURL)

	scope := authorizationRequest.Query().Get("scope")
	require.Contains(k.T(), strings.Fields(scope), "openid",
		"The authorization request should ask for the openid scope so the code it yields comes with an id_token, got scope [%v]", scope)
	require.Subset(k.T(), strings.Fields(scope), strings.Fields(k.client.Auth.KeycloakOIDC.Config.Scopes),
		"The authorization request should carry every configured scope, got scope [%v]", scope)
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package keycloakoidc

import (
	authactions "github.com/rancher/tests/actions/auth"
	tokenapi "github.com/rancher/tests/actions/tokens"
	"github.com/stretchr/testify/require"
)

func (k *KeycloakOIDCAuthProviderSuite) TestKeycloakOIDCLoginTokenRecordsLastUsedAt() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, k.adminUser, authactions.KeycloakOIDC)
	require.NoError(k.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NoError(k.T(), tokenapi.VerifyLoginTokenRecordsLastUsedAt(k.client, authAdmin), "The first use of the KeycloakOIDC login token should be written to its lastUsedAt")
}

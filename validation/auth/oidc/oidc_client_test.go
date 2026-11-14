//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package oidc

import (
	"net/http"

	oidcext "github.com/rancher/shepherd/extensions/auth/oidc"
	"github.com/rancher/shepherd/extensions/kubeapi/auth/oidcclient"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func (s *OIDCTestSuite) TestOIDCClientCreatableViaNormanAPI() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	normanClientName := namegen.AppendRandomString("norman-oidc")
	logrus.Infof("Verifying POST %s creates OIDCClient %s", oidcext.OIDCClientsPath, normanClientName)

	resp, err := s.oidcAPI.RawRequest(http.MethodPost, oidcext.OIDCClientsPath, "Bearer "+s.client.RancherConfig.AdminToken, map[string]interface{}{
		"type":                          "oidcclient",
		"name":                          normanClientName,
		"redirectURIs":                  []string{s.oidcConfig.RedirectURI},
		"scopes":                        s.oidcConfig.Scopes,
		"tokenExpirationSeconds":        s.oidcConfig.TokenExpirationSeconds,
		"refreshTokenExpirationSeconds": s.oidcConfig.RefreshTokenExpirationSeconds,
	})
	require.NoError(s.T(), err)

	if resp.StatusCode == http.StatusCreated {
		subSession.RegisterCleanupFunc(func() error {
			return oidcclient.DeleteOIDCClient(s.client, normanClientName)
		})
	}

	require.Equal(s.T(), http.StatusCreated, resp.StatusCode,
		"POST %s must return 201, got %d: %s", oidcext.OIDCClientsPath, resp.StatusCode, resp.Body)
}

func (s *OIDCTestSuite) TestTokenEndpointWrongClientSecretIsRejected() {
	logrus.Info("Verifying the refresh_token grant rejects a wrong client_secret")

	_, err := s.oidcAPI.RefreshAccessToken(s.tokenSet.RefreshToken, s.clientID, namegen.AppendRandomString("wrong-secret"))
	require.Error(s.T(), err, "refresh_token grant must reject a wrong client_secret")
	require.NotContains(s.T(), err.Error(), "500", "A wrong client_secret must not produce a server error")
}

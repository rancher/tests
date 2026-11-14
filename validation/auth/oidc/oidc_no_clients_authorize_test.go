//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	oidcext "github.com/rancher/shepherd/extensions/auth/oidc"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/kubeapi/auth/oidcclient"
	"github.com/rancher/shepherd/extensions/token"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func (s *OIDCNoClientsTestSuite) TestDiscoveryReturns200WithNoOIDCClients() {
	s.requireNoOIDCClients()

	logrus.Info("Verifying GET /oidc/.well-known/openid-configuration returns 200 with no OIDCClients configured")

	resp, discoveryDoc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Discovery must return 200 with no OIDCClients configured, got %d: %s", resp.StatusCode, resp.Body)

	for _, field := range []string{"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri"} {
		require.Contains(s.T(), discoveryDoc, field,
			"Discovery document must describe the provider with no OIDCClients configured, missing %s", field)
	}
}

func (s *OIDCNoClientsTestSuite) TestJWKSReturns200WithNoOIDCClients() {
	s.requireNoOIDCClients()

	logrus.Info("Verifying GET /oidc/.well-known/jwks.json returns 200 with no OIDCClients configured")

	resp, keySet := s.fetchJWKS()
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"JWKS must return 200 with no OIDCClients configured, got %d: %s", resp.StatusCode, resp.Body)

	keys, ok := keySet["keys"].([]interface{})
	require.True(s.T(), ok, "JWKS document must contain a keys list")
	require.NotEmpty(s.T(), keys, "JWKS must advertise at least one signing key with no OIDCClients configured")

	firstKey, ok := keys[0].(map[string]interface{})
	require.True(s.T(), ok, "JWKS keys must be objects")
	for _, field := range []string{"kid", "kty", "use"} {
		require.Contains(s.T(), firstKey, field, "JWKS key missing required field %s", field)
	}
}

func (s *OIDCNoClientsTestSuite) TestPublicEndpointsSetSecurityHeadersWithNoOIDCClients() {
	s.requireNoOIDCClients()

	logrus.Info("Verifying the public OIDC endpoints set the security headers and are not CORS-readable with no OIDCClients configured")

	securityHeaders := map[string]string{
		"X-Content-Type-Options":       "nosniff",
		"X-Frame-Options":              "SAMEORIGIN",
		"Referrer-Policy":              "strict-origin-when-cross-origin",
		"Strict-Transport-Security":    "max-age=31536000",
		"Access-Control-Allow-Methods": "GET, POST",
	}

	publicEndpoints := []struct{ name, path string }{
		{"discovery", oidcext.OIDCDiscoveryPath},
		{"jwks", oidcJWKSPath},
	}

	for _, endpoint := range publicEndpoints {
		s.T().Run(endpoint.name, func(t *testing.T) {
			resp, err := s.oidcAPI.RawRequest(http.MethodGet, endpoint.path, "", nil)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, resp.StatusCode,
				"%s must return 200 with no OIDCClients configured, got %d: %s", endpoint.path, resp.StatusCode, resp.Body)

			for header, value := range securityHeaders {
				assert.Equal(t, value, resp.Header.Get(header), "%s must set the %s header", endpoint.path, header)
			}

			assert.Empty(t, resp.Header.Get(accessControlAllowOriginHeader),
				"%s must not set %s, which would let any page read the response cross-origin", endpoint.path, accessControlAllowOriginHeader)
		})
	}
}

func (s *OIDCNoClientsTestSuite) TestAuthorizeReturnsClientErrorWithNoOIDCClients() {
	s.requireNoOIDCClients()

	logrus.Info("Verifying /oidc/authorize rejects an unsupported method as a client error instead of a server error")

	resp, err := s.oidcAPI.RawRequest(http.MethodDelete, oidcext.OIDCAuthPath, "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode,
		"The authorize endpoint must reach its own handler with no OIDCClients configured, got %d: %s", resp.StatusCode, resp.Body)

	logrus.Info("Verifying a well-formed authorize request naming an unknown client is rejected with invalid_request")

	authorizeQuery := url.Values{
		"response_type":         {"code"},
		"client_id":             {namegen.AppendRandomString("unknown-client")},
		"redirect_uri":          {s.oidcConfig.RedirectURI},
		"scope":                 {strings.Join(s.oidcConfig.Scopes, " ")},
		"state":                 {namegen.AppendRandomString("state")},
		"code_challenge":        {authorizeCodeChallenge},
		"code_challenge_method": {authorizeCodeChallengeMethod},
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.OneSecondTimeout, defaults.OneMinuteTimeout, true, func(context.Context) (bool, error) {
		loginToken, err := token.GenerateUserToken(&management.User{Username: s.oidcConfig.AdminUsername, Password: s.oidcConfig.AdminPassword},
			s.client.RancherConfig.Host)
		if err != nil {
			return false, err
		}

		subSession.RegisterCleanupFunc(func() error {
			err := s.client.WranglerContext.Mgmt.Token().Delete(loginToken.ID, &metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}

			return err
		})

		resp, err = s.oidcAPI.RawRequest(http.MethodGet, oidcext.OIDCAuthPath+"?"+authorizeQuery.Encode(), "Bearer "+loginToken.Token, nil)
		if err != nil {
			return false, err
		}

		return resp.StatusCode != http.StatusOK, nil
	})
	if !kwait.Interrupted(pollErr) {
		require.NoError(s.T(), pollErr, "The admin should be able to log in to Rancher and send the authorize request, since only a login token can start an OIDC sign-in")
	}

	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode,
		"An authorize request naming an unknown client must be rejected with 400, got %d: %s", resp.StatusCode, resp.Body)

	var authorizeError map[string]string
	require.NoError(s.T(), json.Unmarshal(resp.Body, &authorizeError),
		"The authorize error response must be a JSON OAuth2 error, got %s", resp.Body)
	require.Equal(s.T(), invalidRequestError, authorizeError["error"],
		"An authorize request naming an unknown client must be rejected with %s, got %s", invalidRequestError, resp.Body)
}

func (s *OIDCNoClientsTestSuite) TestTokenEndpointReturns400ForUnverifiableRefreshTokenWithNoOIDCClients() {
	s.requireNoOIDCClients()

	logrus.Info("Verifying the refresh_token grant rejects an unverifiable token with an HTTP 400 status instead of a server error with no OIDCClients configured")

	_, err := s.oidcAPI.RefreshAccessToken(
		unverifiableRefreshToken,
		namegen.AppendRandomString("unknown-client"),
		namegen.AppendRandomString("unknown-secret"),
	)
	require.Error(s.T(), err, "The refresh_token grant must reject a token it cannot verify")
	require.NotContains(s.T(), err.Error(), strconv.Itoa(http.StatusInternalServerError),
		"An unverifiable refresh token must be rejected with a client error status, not a server error, got %v", err)
	require.Contains(s.T(), err.Error(), strconv.Itoa(http.StatusBadRequest),
		"An unverifiable refresh token must be rejected with 400, got %v", err)
}

func (s *OIDCNoClientsTestSuite) TestUserInfoRejectsInvalidTokenWithNoOIDCClients() {
	s.requireNoOIDCClients()

	logrus.Info("Verifying /oidc/userinfo returns an OAuth2 error instead of a server error with no OIDCClients configured")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcUserInfoPath, "Bearer "+namegen.AppendRandomString("not-a-jwt"), nil)
	require.NoError(s.T(), err)
	require.Contains(s.T(), []int{http.StatusBadRequest, http.StatusUnauthorized}, resp.StatusCode,
		"An invalid access token must be rejected as a client error, not a server error, got %d: %s", resp.StatusCode, resp.Body)

	var userInfoError map[string]string
	require.NoError(s.T(), json.Unmarshal(resp.Body, &userInfoError),
		"The userinfo error response must be a JSON OAuth2 error, got %s", resp.Body)
	require.Equal(s.T(), invalidRequestError, userInfoError["error"],
		"An invalid access token must be rejected with %s, got %s", invalidRequestError, resp.Body)
}

func (s *OIDCNoClientsTestSuite) TestPublicEndpointsStayAvailableAfterLastOIDCClientDeleted() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	s.requireNoOIDCClients()

	oidcClientName, _, _ := s.createOIDCClient(subSession, "last-oidc-client")

	logrus.Info("Verifying discovery returns 200 while an OIDCClient exists")
	resp, _, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Discovery must return 200 while an OIDCClient exists, got %d: %s", resp.StatusCode, resp.Body)

	logrus.Infof("Deleting the last OIDCClient %s", oidcClientName)
	require.NoError(s.T(), oidcclient.DeleteOIDCClient(s.client, oidcClientName), "Failed to delete OIDCClient %s", oidcClientName)
	s.requireNoOIDCClients()

	logrus.Info("Verifying discovery still returns 200 after the last OIDCClient is deleted")
	resp, discoveryDoc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Discovery must return 200 after the last OIDCClient is deleted, got %d: %s", resp.StatusCode, resp.Body)
	require.Contains(s.T(), discoveryDoc, "issuer", "Discovery document must still describe the provider")
	require.Empty(s.T(), resp.Header.Get(accessControlAllowOriginHeader),
		"Discovery must not set %s after the last OIDCClient is deleted", accessControlAllowOriginHeader)

	logrus.Info("Verifying JWKS still returns 200 after the last OIDCClient is deleted")
	jwksResp, keySet := s.fetchJWKS()
	require.Equal(s.T(), http.StatusOK, jwksResp.StatusCode,
		"JWKS must return 200 after the last OIDCClient is deleted, got %d: %s", jwksResp.StatusCode, jwksResp.Body)
	require.NotEmpty(s.T(), keySet["keys"], "JWKS must still advertise a signing key after the last OIDCClient is deleted")
}

func (s *OIDCNoClientsTestSuite) TestRefreshTokenGrantReturnsInvalidGrantAfterLastOIDCClientDeleted() {
	s.T().Skip("Known issue: https://github.com/rancher/rancher/issues/58801")

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	tokenSet, clientID, clientSecret := s.issueTokenSetAndDeleteLastOIDCClient(subSession, "revoked-refresh-token-client")
	s.requireAccessTokenRevoked(tokenSet.AccessToken)

	logrus.Info("Verifying the refresh_token grant returns 400 invalid_grant for the revoked refresh token")
	_, err := s.oidcAPI.RefreshAccessToken(tokenSet.RefreshToken, clientID, clientSecret)
	require.Error(s.T(), err, "The refresh_token grant must reject a revoked refresh token")
	require.NotContains(s.T(), err.Error(), strconv.Itoa(http.StatusInternalServerError),
		"A revoked refresh token must be rejected as a client error, not a server error, got %v", err)
	require.Contains(s.T(), err.Error(), strconv.Itoa(http.StatusBadRequest),
		"A revoked refresh token must be rejected with 400, got %v", err)
	require.Contains(s.T(), err.Error(), invalidGrantError,
		"A revoked refresh token must be rejected with the invalid_grant OAuth2 error, got %v", err)
}

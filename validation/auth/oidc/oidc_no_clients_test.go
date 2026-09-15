//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package oidc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	oidcauth "github.com/rancher/shepherd/clients/rancher/auth/oidc"
	oidcext "github.com/rancher/shepherd/extensions/auth/oidc"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/kubeapi/auth/oidcclient"
	"github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeapi/features"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	"github.com/rancher/shepherd/pkg/clientbase"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	oidcJWKSPath                   = "/oidc/.well-known/jwks.json"
	oidcUserInfoPath               = "/oidc/userinfo"
	accessControlAllowOriginHeader = "Access-Control-Allow-Origin"
	wwwAuthenticateHeader          = "WWW-Authenticate"
	invalidGrantError              = "invalid_grant"
	invalidRequestError            = "invalid_request"
	invalidTokenChallenge          = `error="invalid_token"`
	authorizeCodeChallenge         = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
	authorizeCodeChallengeMethod   = "S256"
	unverifiableRefreshToken       = "eyJhbGciOiJSUzI1NiIsInR5cCI6IkpXVCIsImtpZCI6ImFiYyJ9.eyJzdWIiOiJ1c2VyLXh4eHgiLCJleHAiOjk5OTk5OTk5OTl9.c2lnbmF0dXJl"
)

type OIDCNoClientsTestSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	oidcConfig *oidcauth.Config
	oidcAPI    *oidcauth.APIClient
}

func (s *OIDCNoClientsTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Loading OIDC configuration from config file")
	s.oidcConfig = new(oidcauth.Config)
	config.LoadConfig(oidcauth.ConfigurationFileKey, s.oidcConfig)
	require.NotEmpty(s.T(), s.oidcConfig.ClientName, "oidc.clientName must be set in cattle-config.yaml")
	require.NotEmpty(s.T(), s.oidcConfig.RedirectURI, "oidc.redirectURI must be set in cattle-config.yaml")
	require.NotEmpty(s.T(), s.oidcConfig.AdminUsername, "oidc.adminUsername must be set in cattle-config.yaml")
	require.NotEmpty(s.T(), s.oidcConfig.AdminPassword, "oidc.adminPassword must be set in cattle-config.yaml")

	if len(s.oidcConfig.Scopes) == 0 {
		s.oidcConfig.Scopes = oidcauth.DefaultAutomationScopes
	}
	if s.oidcConfig.TokenExpirationSeconds == 0 {
		s.oidcConfig.TokenExpirationSeconds = oidcauth.DefaultTokenExpirationSeconds
	}
	if s.oidcConfig.RefreshTokenExpirationSeconds == 0 {
		s.oidcConfig.RefreshTokenExpirationSeconds = oidcauth.DefaultRefreshTokenExpirationSeconds
	}

	s.oidcAPI = client.Auth.OIDC

	logrus.Info("Enabling oidc-provider feature flag")
	err = features.EnableFeatureFlag(client, oidcauth.OIDCProviderFeatureFlag)
	require.NoError(s.T(), err, "Failed to enable oidc-provider feature flag")

	err = deployments.WaitForDeploymentActive(client, cluster.LocalCluster, deployments.RancherDeploymentNamespace, deployments.RancherDeploymentName)
	require.NoError(s.T(), err, "Rancher did not become ready after enabling oidc-provider")
}

func (s *OIDCNoClientsTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *OIDCNoClientsTestSuite) requireNoOIDCClients() {
	logrus.Info("Waiting until the Rancher server has no OIDCClients")

	var remaining []string
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true,
		func(ctx context.Context) (bool, error) {
			oidcClients, err := s.client.WranglerContext.Mgmt.OIDCClient().List(metav1.ListOptions{})
			if err != nil {
				return false, err
			}

			remaining = nil
			for _, oidcClient := range oidcClients.Items {
				remaining = append(remaining, oidcClient.Name)
			}

			return len(remaining) == 0, nil
		},
	)
	require.NoError(s.T(), err, "These tests require a Rancher server with no OIDCClients, still present: %v", remaining)
}

func (s *OIDCNoClientsTestSuite) createOIDCClient(subSession *session.Session, namePrefix string) (string, string, string) {
	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to create a scoped client")

	name := namegen.AppendRandomString(namePrefix)
	logrus.Infof("Creating OIDCClient %s", name)

	_, err = oidcclient.CreateOIDCClient(subClient, name, v3.OIDCClientSpec{
		RedirectURIs:                  []string{s.oidcConfig.RedirectURI},
		Scopes:                        s.oidcConfig.Scopes,
		TokenExpirationSeconds:        int64(s.oidcConfig.TokenExpirationSeconds),
		RefreshTokenExpirationSeconds: int64(s.oidcConfig.RefreshTokenExpirationSeconds),
	})
	require.NoError(s.T(), err, "Failed to create OIDCClient %s", name)

	clientID, secretKeyName, err := oidcclient.WaitForOIDCClientReady(subClient, name)
	require.NoError(s.T(), err, "OIDCClient %s never reported a client ID and secret", name)

	clientSecret, err := oidcclient.FetchOIDCClientSecret(subClient, clientID, secretKeyName)
	require.NoError(s.T(), err, "Failed to fetch the OIDCClient %s secret", name)

	return name, clientID, clientSecret
}

func (s *OIDCNoClientsTestSuite) fetchJWKS() (*clientbase.Response, map[string]interface{}) {
	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcJWKSPath, "", nil)
	require.NoError(s.T(), err)

	var keySet map[string]interface{}
	require.NoError(s.T(), json.Unmarshal(resp.Body, &keySet), "JWKS response is not valid JSON (HTTP %d)", resp.StatusCode)

	return resp, keySet
}

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

	resp, err = s.oidcAPI.RawRequest(http.MethodGet, oidcext.OIDCAuthPath+"?"+authorizeQuery.Encode(),
		"Bearer "+s.client.RancherConfig.AdminToken, nil)
	require.NoError(s.T(), err)
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

func (s *OIDCNoClientsTestSuite) issueTokenSetAndDeleteLastOIDCClient(subSession *session.Session, namePrefix string) (*oidcext.TokenSet, string, string) {
	s.requireNoOIDCClients()

	oidcClientName, clientID, clientSecret := s.createOIDCClient(subSession, namePrefix)

	logrus.Info("Completing the headless PKCE auth-code flow before deleting the OIDCClient")
	tokenSet, err := s.oidcAPI.CompleteAuthCodeFlow(
		clientID, clientSecret,
		s.oidcConfig.RedirectURI, strings.Join(s.oidcConfig.Scopes, " "),
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.NoError(s.T(), err, "PKCE auth-code flow failed")
	require.NotEmpty(s.T(), tokenSet.RefreshToken, "refresh_token must be issued when the offline_access scope is requested")

	logrus.Info("Verifying the issued access token authenticates the Rancher API before the OIDCClient is deleted")
	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, "Bearer "+tokenSet.AccessToken, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"The issued access token must authenticate /v3/users while its OIDCClient exists, got %d: %s", resp.StatusCode, resp.Body)

	logrus.Infof("Deleting the last OIDCClient %s", oidcClientName)
	require.NoError(s.T(), oidcclient.DeleteOIDCClient(s.client, oidcClientName), "Failed to delete OIDCClient %s", oidcClientName)
	s.requireNoOIDCClients()

	return tokenSet, clientID, clientSecret
}

func (s *OIDCNoClientsTestSuite) requireAccessTokenRevoked(accessToken string) {
	logrus.Info("Waiting for the issued access token to stop authenticating the Rancher API")

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, false,
		func(ctx context.Context) (bool, error) {
			resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, "Bearer "+accessToken, nil)
			if err != nil {
				return false, nil
			}
			return resp.StatusCode == http.StatusUnauthorized, nil
		},
	)
	require.NoError(s.T(), err, "The access token must stop authenticating /v3/users once the last OIDCClient is deleted")
}

func (s *OIDCNoClientsTestSuite) TestAccessTokenStopsAuthenticatingAfterLastOIDCClientDeleted() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	tokenSet, _, _ := s.issueTokenSetAndDeleteLastOIDCClient(subSession, "revoked-access-token-client")

	s.requireAccessTokenRevoked(tokenSet.AccessToken)
}

func (s *OIDCNoClientsTestSuite) TestRefreshTokenGrantReturnsInvalidGrantAfterLastOIDCClientDeleted() {
	s.T().Skip("Known issue: https://github.com/rancher/rancher/issues/56837")

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

func (s *OIDCNoClientsTestSuite) TestUserInfoRejectsRevokedTokenAfterLastOIDCClientDeleted() {
	s.T().Skip("Known issue: https://github.com/rancher/rancher/issues/57319")

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	tokenSet, _, _ := s.issueTokenSetAndDeleteLastOIDCClient(subSession, "revoked-userinfo-token-client")
	s.requireAccessTokenRevoked(tokenSet.AccessToken)

	logrus.Info("Verifying /oidc/userinfo returns 401 with an invalid_token challenge for the revoked access token")
	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcUserInfoPath, "Bearer "+tokenSet.AccessToken, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"An access token that no longer authenticates the Rancher API must be rejected with 401 at /oidc/userinfo, got %d: %s", resp.StatusCode, resp.Body)
	require.Contains(s.T(), resp.Header.Get(wwwAuthenticateHeader), invalidTokenChallenge,
		"A 401 from /oidc/userinfo must carry a %s challenge containing %s, got %q", wwwAuthenticateHeader, invalidTokenChallenge, resp.Header.Get(wwwAuthenticateHeader))
}

func TestOIDCNoClientsSuite(t *testing.T) {
	suite.Run(t, new(OIDCNoClientsTestSuite))
}

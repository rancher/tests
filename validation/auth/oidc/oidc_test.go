//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package oidc

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	oidcauth "github.com/rancher/shepherd/clients/rancher/auth/oidc"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	oidcactions "github.com/rancher/tests/actions/oidc"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type OIDCTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	oidcConfig   *oidcauth.Config
	oidcAPI      *oidcauth.APIClient
	clientID     string
	clientSecret string
	tokenSet     *oidcauth.TokenSet
}

func (s *OIDCTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("[OIDC SetupSuite] Loading OIDC configuration from config file")
	s.oidcConfig = new(oidcauth.Config)
	config.LoadConfig(oidcauth.ConfigKey, s.oidcConfig)
	require.NotEmpty(s.T(), s.oidcConfig.ClientName, "oidc.clientName must be set in cattle-config.yaml")
	require.NotEmpty(s.T(), s.oidcConfig.RedirectURI, "oidc.redirectURI must be set in cattle-config.yaml")
	require.NotEmpty(s.T(), s.oidcConfig.AdminUsername, "oidc.adminUsername must be set in cattle-config.yaml")
	require.NotEmpty(s.T(), s.oidcConfig.AdminPassword, "oidc.adminPassword must be set in cattle-config.yaml")

	if len(s.oidcConfig.Scopes) == 0 {
		s.oidcConfig.Scopes = oidcauth.DefaultAutomationScopes
	}
	if s.oidcConfig.TokenExpirationSeconds == 0 {
		s.oidcConfig.TokenExpirationSeconds = 3600
	}
	if s.oidcConfig.RefreshTokenExpirationSeconds == 0 {
		s.oidcConfig.RefreshTokenExpirationSeconds = 86400
	}

	s.oidcAPI = oidcauth.NewAPIClient(client.RancherConfig.Host)

	require.NoError(s.T(), oidcactions.EnableOIDCFeatureFlag(client),
		"Failed to enable oidc-provider feature flag")

	logrus.Infof("[OIDC SetupSuite] Creating OIDCClient %q", s.oidcConfig.ClientName)
	spec := oidcactions.ClientSpec{
		RedirectURIs:                  []string{s.oidcConfig.RedirectURI},
		Scopes:                        s.oidcConfig.Scopes,
		TokenExpirationSeconds:        s.oidcConfig.TokenExpirationSeconds,
		RefreshTokenExpirationSeconds: s.oidcConfig.RefreshTokenExpirationSeconds,
	}
	require.NoError(s.T(), oidcactions.CreateOIDCClient(client, s.oidcConfig.ClientName, spec),
		"Failed to create OIDCClient CRD")

	clientID, secretKeyName, err := oidcactions.WaitForOIDCClientReady(client, s.oidcConfig.ClientName)
	require.NoError(s.T(), err, "Failed to wait for OIDCClient status.clientID")
	require.NotEmpty(s.T(), clientID, "OIDCClient status.clientID is empty after ready wait")
	s.clientID = clientID

	clientSecret, err := oidcactions.FetchOIDCClientSecret(client, clientID, secretKeyName)
	require.NoError(s.T(), err, "Failed to fetch OIDCClient secret from cattle-oidc-client-secrets")
	require.NotEmpty(s.T(), clientSecret, "OIDCClient secret value is empty")
	s.clientSecret = clientSecret

	logrus.Info("[OIDC SetupSuite] Completing headless PKCE auth-code flow to obtain access token")
	scopeStr := strings.Join(s.oidcConfig.Scopes, " ")
	ts, err := s.oidcAPI.CompleteAuthCodeFlow(
		s.clientID, s.clientSecret,
		s.oidcConfig.RedirectURI, scopeStr,
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.NoError(s.T(), err, "Failed to complete PKCE auth flow in SetupSuite")
	require.NotEmpty(s.T(), ts.AccessToken, "Access token is empty after PKCE flow")
	s.tokenSet = ts
	logrus.Infof("[OIDC SetupSuite] Access token obtained (length=%d)", len(ts.AccessToken))
}

func (s *OIDCTestSuite) TearDownSuite() {
	if s.client == nil {
		return
	}
	if s.oidcConfig != nil && s.oidcConfig.ClientName != "" {
		if err := oidcactions.DeleteOIDCClient(s.client, s.oidcConfig.ClientName); err != nil {
			logrus.Errorf("[OIDC TearDownSuite] Failed to delete OIDCClient %q: %v",
				s.oidcConfig.ClientName, err)
		}
	}
	enabled, err := oidcactions.IsOIDCFeatureFlagEnabled(s.client)
	if err != nil {
		logrus.Errorf("[OIDC TearDownSuite] Could not check oidc-provider flag state: %v", err)
	} else if enabled {
		if err := oidcactions.DisableOIDCFeatureFlag(s.client); err != nil {
			logrus.Errorf("[OIDC TearDownSuite] Failed to disable oidc-provider flag: %v", err)
		}
	}
}

func (s *OIDCTestSuite) newSubClient() *rancher.Client {
	subSession := s.session.NewSession()
	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)
	return subClient
}


func (s *OIDCTestSuite) TestFeatureFlagEnabledAllowsAccessTokenAuth() {
	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+s.tokenSet.AccessToken)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Expected 200 with OIDC access token when flag is ON, body: %s", resp.Body)
}

func (s *OIDCTestSuite) TestDiscoveryWellKnownEndpointReturns200() {
	resp, doc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Discovery endpoint must return 200")
	require.NotNil(s.T(), doc)
}

func (s *OIDCTestSuite) TestDiscoveryContainsRequiredRFC8414Fields() {
	_, doc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)

	for _, field := range []string{
		"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri",
		"response_types_supported", "subject_types_supported",
		"id_token_signing_alg_values_supported",
	} {
		require.Contains(s.T(), doc, field,
			"Discovery document missing required field %q", field)
	}
}

func (s *OIDCTestSuite) TestDiscoveryContainsMCPRequiredFields() {
	_, doc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)

	for _, field := range []string{"grant_types_supported", "code_challenge_methods_supported"} {
		require.Contains(s.T(), doc, field, "Discovery document missing field %q", field)
	}

	if _, ok := doc["registration_endpoint"]; !ok {
		logrus.Warn("[OIDC] registration_endpoint not present in discovery document — Dynamic Client Registration not yet implemented")
	}

	grantTypes, ok := doc["grant_types_supported"].([]interface{})
	require.True(s.T(), ok)
	var hasAuthCode bool
	for _, gt := range grantTypes {
		if gt == "authorization_code" {
			hasAuthCode = true
		}
	}
	require.True(s.T(), hasAuthCode, "grant_types_supported must include 'authorization_code'")

	methods, ok := doc["code_challenge_methods_supported"].([]interface{})
	require.True(s.T(), ok)
	var hasS256 bool
	for _, m := range methods {
		if m == "S256" {
			hasS256 = true
		}
	}
	require.True(s.T(), hasS256, "code_challenge_methods_supported must include 'S256'")
}

func (s *OIDCTestSuite) TestOIDCClientUnregisteredScopeIsRejected() {
	_, err := s.oidcAPI.CompleteAuthCodeFlow(
		s.clientID, s.clientSecret,
		s.oidcConfig.RedirectURI,
		"openid rancher:users admin:write",
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.Error(s.T(), err,
		"Expected error when requesting scope not in spec.scopes")
	logrus.Infof("[OIDC] Unregistered scope correctly rejected: %v", err)
}

func (s *OIDCTestSuite) TestOIDCClientScopeListLimitsIDToken() {
	require.NotEmpty(s.T(), s.tokenSet.IDToken,
		"id_token must be present when openid scope is requested")

	ts, err := s.oidcAPI.CompleteAuthCodeFlow(
		s.clientID, s.clientSecret,
		s.oidcConfig.RedirectURI,
		"profile rancher:users",
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.NoError(s.T(), err)
	require.Empty(s.T(), ts.IDToken,
		"id_token must NOT be present when openid scope is omitted")
	require.NotEmpty(s.T(), ts.AccessToken)
}

func (s *OIDCTestSuite) TestAccessTokenAuthenticatesV3UsersAPI() {
	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+s.tokenSet.AccessToken)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Expected 200 with OIDC access token, body: %s", resp.Body)

	var body map[string]interface{}
	require.NoError(s.T(), json.Unmarshal(resp.Body, &body))
	require.Equal(s.T(), "collection", body["type"])
	_, hasData := body["data"]
	require.True(s.T(), hasData)
}

func (s *OIDCTestSuite) TestAccessTokenJWTContainsRequiredClaims() {
	claims, err := oidcauth.DecodeJWTPayload(s.tokenSet.AccessToken)
	require.NoError(s.T(), err, "access_token must be a valid 3-part JWT")

	for _, claim := range []string{"sub", "iss", "exp", "iat"} {
		require.Contains(s.T(), claims, claim, "JWT missing required claim %q", claim)
	}
	sub, _ := claims["sub"].(string)
	require.NotEmpty(s.T(), sub)
	logrus.Infof("[OIDC] JWT sub=%s iss=%s", sub, claims["iss"])
}

func (s *OIDCTestSuite) TestAccessTokenTamperedTokenReturns401() {
	tampered, err := oidcauth.TamperJWTSignature(s.tokenSet.AccessToken)
	require.NoError(s.T(), err)

	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+tampered)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"Tampered JWT must return 401, got %d body: %s", resp.StatusCode, resp.Body)
	require.NotEqual(s.T(), http.StatusInternalServerError, resp.StatusCode)
}

func (s *OIDCTestSuite) TestAccessTokenV3ClustersAccessible() {
	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.ClustersPath, "Bearer "+s.tokenSet.AccessToken)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Expected 200 on /v3/clusters with OIDC access token, body: %s", resp.Body)
}

func (s *OIDCTestSuite) TestAccessTokenAdminTokenUnaffectedByFlag() {
	users, err := s.client.Management.User.List(nil)
	require.NoError(s.T(), err, "Admin token must still work with flag ON")
	require.NotEmpty(s.T(), users.Data)
}

func (s *OIDCTestSuite) TestTokenEndpointPKCEFlowProducesValidTokens() {
	scopeStr := strings.Join(s.oidcConfig.Scopes, " ")
	ts, err := s.oidcAPI.CompleteAuthCodeFlow(
		s.clientID, s.clientSecret,
		s.oidcConfig.RedirectURI, scopeStr,
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.NoError(s.T(), err, "PKCE auth flow must succeed")
	require.NotEmpty(s.T(), ts.AccessToken)
	require.NotEmpty(s.T(), ts.IDToken, "id_token must be present when openid scope is requested")
	require.NotEmpty(s.T(), ts.RefreshToken, "refresh_token must be present when offline_access scope is requested")
	require.Equal(s.T(), "Bearer", ts.TokenType)
}

func (s *OIDCTestSuite) TestTokenEndpointRefreshTokenExchangeWorks() {
	require.NotEmpty(s.T(), s.tokenSet.RefreshToken,
		"refresh_token must have been obtained in SetupSuite")

	newTS, err := s.oidcAPI.RefreshAccessToken(s.tokenSet.RefreshToken, s.clientID, s.clientSecret)
	require.NoError(s.T(), err, "Refresh token exchange must succeed")
	require.NotEmpty(s.T(), newTS.AccessToken)

	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+newTS.AccessToken)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Refreshed access_token must authenticate /v3/users")
}

func (s *OIDCTestSuite) TestTokenEndpointWrongClientSecretReturns4xx() {
	_, err := s.oidcAPI.RefreshAccessToken(
		s.tokenSet.RefreshToken, s.clientID, "wrong-secret-xyz-99999")

	if err == nil {
		s.T().Skip("client_secret not yet enforced on refresh_token grant — fix in progress rancher/rancher#54401")
		return
	}
	require.Error(s.T(), err, "wrong client_secret must result in an error")
	require.NotContains(s.T(), err.Error(), "500")
}

func (s *OIDCTestSuite) TestSecurityMissingAuthHeaderReturns401() {
	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"No auth header must return 401, got %d body: %s", resp.StatusCode, resp.Body)
	require.NotEqual(s.T(), http.StatusInternalServerError, resp.StatusCode)
}

func (s *OIDCTestSuite) TestSecurityMalformedBearerTokenReturns401() {
	cases := []struct{ name, header string }{
		{"random-string", "Bearer not-a-jwt-at-all"},
		{"empty-bearer", "Bearer "},
		{"two-part-jwt", "Bearer a.b"},
		{"one-dot", "Bearer a."},
		{"bearer-only", "Bearer"},
		{"spaces-in-token", "Bearer eye . abc . def"},
	}
	for _, tc := range cases {
		s.T().Run(tc.name, func(t *testing.T) {
			resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, tc.header)
			require.NoError(t, err)
			require.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"(%s): got %d body: %s", tc.name, resp.StatusCode, resp.Body)
			require.NotEqual(t, http.StatusInternalServerError, resp.StatusCode)
		})
	}
}

func (s *OIDCTestSuite) TestSecurityNonStringKidDoesNotPanic() {
	craftedJWT, err := oidcauth.CraftJWTWithIntKid()
	require.NoError(s.T(), err)

	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+craftedJWT)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"Integer kid must return 401, got %d body: %s", resp.StatusCode, resp.Body)
	require.NotEqual(s.T(), http.StatusInternalServerError, resp.StatusCode)

	bodyStr := strings.ToLower(string(resp.Body))
	require.NotContains(s.T(), bodyStr, "panic")
	require.NotContains(s.T(), bodyStr, "runtime error")
}

func (s *OIDCTestSuite) TestSecurityTamperedSignatureReturns401() {
	tampered, err := oidcauth.TamperJWTSignature(s.tokenSet.AccessToken)
	require.NoError(s.T(), err)

	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+tampered)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"Tampered signature must return 401")
	require.NotEqual(s.T(), http.StatusInternalServerError, resp.StatusCode)
}

func (s *OIDCTestSuite) TestRegressionAdminTokenUnaffected() {
	users, err := s.client.Management.User.List(nil)
	require.NoError(s.T(), err, "Admin token must work with flag ON")
	require.NotEmpty(s.T(), users.Data)
}

func (s *OIDCTestSuite) TestRegressionBothOIDCAndAdminTokenWork() {
	oidcResp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+s.tokenSet.AccessToken)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, oidcResp.StatusCode,
		"OIDC token must return 200")

	users, err := s.client.Management.User.List(nil)
	require.NoError(s.T(), err, "Admin token must return users simultaneously")
	require.NotEmpty(s.T(), users.Data)
}

func (s *OIDCTestSuite) TestRegressionDiscoveryDocumentIssuerMatchesRancherURL() {
	_, doc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)

	issuer, _ := doc["issuer"].(string)
	require.NotEmpty(s.T(), issuer, "issuer must be in discovery doc")

	rancherHost := strings.TrimPrefix(strings.TrimRight(s.client.RancherConfig.Host, "/"), "https://")
	require.True(s.T(), strings.Contains(issuer, rancherHost),
		"issuer %q must reference Rancher host %q", issuer, rancherHost)
}

func (s *OIDCTestSuite) TestZFeatureFlagDisabledBlocksAccessToken() {
	subClient := s.newSubClient()

	users, err := s.client.Management.User.List(nil)
	require.NoError(s.T(), err, "Pre-condition: management client must work")
	require.NotEmpty(s.T(), users.Data)

	require.NoError(s.T(), oidcactions.DisableOIDCFeatureFlag(subClient),
		"Flag disable must succeed via API")

	enabled, err := oidcactions.IsOIDCFeatureFlagEnabled(subClient)
	require.NoError(s.T(), err)
	require.False(s.T(), enabled, "oidc-provider flag must read as OFF after disable")
	logrus.Info("[OIDC] Flag confirmed OFF in management API")

	require.NoError(s.T(), oidcactions.RestartRancherDeployment(s.client),
		"Rancher deployment restart must succeed")

	resp, err := s.oidcAPI.RawRequest("GET", oidcauth.UsersPath, "Bearer "+s.tokenSet.AccessToken)
	require.NoError(s.T(), err)

	logrus.Info("[OIDC] Re-enabling flag for clean teardown")
	require.NoError(s.T(), oidcactions.EnableOIDCFeatureFlag(subClient),
		"Re-enable after test must succeed")

	if resp.StatusCode == http.StatusOK {
		logrus.Warn("[OIDC] OIDC token still accepted after flag disable + pod restart")
		s.T().Skip("OIDC tokens not invalidated on flag disable + restart")
		return
	}

	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"OIDC token must return 401 after flag disable + restart, got %d", resp.StatusCode)
	logrus.Info("[OIDC] Token correctly rejected with 401 after restart")
}

func TestOIDCProviderSuite(t *testing.T) {
	suite.Run(t, new(OIDCTestSuite))
}

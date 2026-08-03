//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package oidc

import (
	"context"
	"encoding/json"
	"net/http"
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
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type OIDCTestSuite struct {
	suite.Suite
	session           *session.Session
	client            *rancher.Client
	oidcConfig        *oidcauth.Config
	oidcAPI           *oidcauth.APIClient
	scopes            string
	oidcClientName    string
	clientID          string
	secretKeyName     string
	clientSecret      string
	tokenSet          *oidcext.TokenSet
	accessTokenHeader string
}

func (s *OIDCTestSuite) SetupSuite() {
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
	s.scopes = strings.Join(s.oidcConfig.Scopes, " ")

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

	s.oidcClientName = namegen.AppendRandomString(s.oidcConfig.ClientName)
	logrus.Infof("Creating OIDCClient %s", s.oidcClientName)
	_, err = oidcclient.CreateOIDCClient(client, s.oidcClientName, v3.OIDCClientSpec{
		RedirectURIs:                  []string{s.oidcConfig.RedirectURI},
		Scopes:                        s.oidcConfig.Scopes,
		TokenExpirationSeconds:        int64(s.oidcConfig.TokenExpirationSeconds),
		RefreshTokenExpirationSeconds: int64(s.oidcConfig.RefreshTokenExpirationSeconds),
	})
	require.NoError(s.T(), err, "Failed to create OIDCClient")

	clientID, secretKeyName, err := oidcclient.WaitForOIDCClientReady(client, s.oidcClientName)
	require.NoError(s.T(), err, "OIDCClient never reported a client ID and secret")
	s.clientID = clientID
	s.secretKeyName = secretKeyName

	clientSecret, err := oidcclient.FetchOIDCClientSecret(client, clientID, secretKeyName)
	require.NoError(s.T(), err, "Failed to fetch OIDCClient secret")
	require.NotEmpty(s.T(), clientSecret, "OIDCClient secret value is empty")
	s.clientSecret = clientSecret

	s.setSuiteTokenSet()
}

func (s *OIDCTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *OIDCTestSuite) setSuiteTokenSet() {
	s.tokenSet = s.completeAuthCodeFlow(s.scopes)
	s.accessTokenHeader = "Bearer " + s.tokenSet.AccessToken
}

func (s *OIDCTestSuite) completeAuthCodeFlow(scopes string) *oidcext.TokenSet {
	logrus.Infof("Completing headless PKCE auth-code flow with scopes %q", scopes)

	tokenSet, err := s.oidcAPI.CompleteAuthCodeFlow(
		s.clientID, s.clientSecret,
		s.oidcConfig.RedirectURI, scopes,
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.NoError(s.T(), err, "PKCE auth-code flow failed")
	require.NotEmpty(s.T(), tokenSet.AccessToken, "PKCE auth-code flow returned an empty access token")

	return tokenSet
}

func (s *OIDCTestSuite) TestDiscoveryWellKnownEndpointReturns200() {
	logrus.Info("Verifying GET /.well-known/openid-configuration returns 200")

	resp, discoveryDoc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "Discovery endpoint must return 200")
	require.NotEmpty(s.T(), discoveryDoc, "Discovery document must not be empty")
}

func (s *OIDCTestSuite) TestDiscoveryContainsRequiredRFC8414Fields() {
	logrus.Info("Verifying discovery document contains required RFC 8414 fields")

	_, discoveryDoc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)

	for _, field := range []string{
		"issuer", "authorization_endpoint", "token_endpoint", "jwks_uri",
		"response_types_supported", "subject_types_supported",
		"id_token_signing_alg_values_supported",
	} {
		require.Contains(s.T(), discoveryDoc, field, "Discovery document missing required RFC 8414 field %s", field)
	}
}

func (s *OIDCTestSuite) TestDiscoveryContainsMCPRequiredFields() {
	logrus.Info("Verifying discovery document advertises authorization_code and S256")

	_, discoveryDoc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)

	grantTypes, ok := discoveryDoc["grant_types_supported"].([]interface{})
	require.True(s.T(), ok, "Discovery document must contain grant_types_supported as a list")
	require.Contains(s.T(), grantTypes, "authorization_code", "grant_types_supported must include authorization_code")

	challengeMethods, ok := discoveryDoc["code_challenge_methods_supported"].([]interface{})
	require.True(s.T(), ok, "Discovery document must contain code_challenge_methods_supported as a list")
	require.Contains(s.T(), challengeMethods, "S256", "code_challenge_methods_supported must include S256")

	if _, ok := discoveryDoc["registration_endpoint"]; !ok {
		logrus.Warn("registration_endpoint not present in discovery document, Dynamic Client Registration is not implemented")
	}
}

func (s *OIDCTestSuite) TestDiscoveryIssuerMatchesRancherURL() {
	logrus.Info("Verifying discovery document issuer references the Rancher host")

	_, discoveryDoc, err := s.oidcAPI.GetDiscovery()
	require.NoError(s.T(), err)

	issuer, ok := discoveryDoc["issuer"].(string)
	require.True(s.T(), ok, "Discovery document issuer must be a string")

	rancherHost := strings.TrimPrefix(strings.TrimRight(s.client.RancherConfig.Host, "/"), "https://")
	require.Contains(s.T(), issuer, rancherHost, "Issuer %s must reference Rancher host %s", issuer, rancherHost)
}

func (s *OIDCTestSuite) TestOIDCClientUnregisteredScopeIsRejected() {
	logrus.Info("Verifying the auth flow rejects a scope that is not registered in the OIDCClient spec")

	_, err := s.oidcAPI.CompleteAuthCodeFlow(
		s.clientID, s.clientSecret,
		s.oidcConfig.RedirectURI,
		"openid rancher:users admin:write",
		s.oidcConfig.AdminUsername, s.oidcConfig.AdminPassword,
	)
	require.Error(s.T(), err, "Requesting a scope outside spec.scopes must fail")
}

func (s *OIDCTestSuite) TestOIDCClientOmittingOpenIDScopeOmitsIDToken() {
	logrus.Info("Verifying id_token is issued only when the openid scope is requested")

	require.NotEmpty(s.T(), s.tokenSet.IDToken, "id_token must be present when the openid scope is requested")

	tokenSet := s.completeAuthCodeFlow("profile rancher:users")
	require.Empty(s.T(), tokenSet.IDToken, "id_token must be absent when the openid scope is omitted")
}

func (s *OIDCTestSuite) TestOIDCClientSchemaRegisteredInNormanAPI() {
	logrus.Info("Verifying the oidcclient schema is registered in the Norman API")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.OIDCClientSchemaPath, "Bearer "+s.client.RancherConfig.AdminToken, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"GET %s must return 200, got %d: %s", oidcext.OIDCClientSchemaPath, resp.StatusCode, resp.Body)
}

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

func (s *OIDCTestSuite) TestOIDCClientRejectsExpirationBelowSchemaMinimum() {
	logrus.Info("Verifying the OIDCClient schema rejects expiration values below the allowed minimum of one second")

	invalidExpirationCases := []struct {
		name                          string
		tokenExpirationSeconds        int64
		refreshTokenExpirationSeconds int64
	}{
		{"zero-token-expiration", 0, int64(s.oidcConfig.RefreshTokenExpirationSeconds)},
		{"negative-token-expiration", -1, int64(s.oidcConfig.RefreshTokenExpirationSeconds)},
		{"zero-refresh-token-expiration", int64(s.oidcConfig.TokenExpirationSeconds), 0},
		{"negative-refresh-token-expiration", int64(s.oidcConfig.TokenExpirationSeconds), -1},
	}

	for _, testCase := range invalidExpirationCases {
		s.T().Run(testCase.name, func(t *testing.T) {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(t, err, "Failed to create a scoped client for case %s", testCase.name)

			_, err = oidcclient.CreateOIDCClient(subClient, namegen.AppendRandomString(testCase.name), v3.OIDCClientSpec{
				RedirectURIs:                  []string{s.oidcConfig.RedirectURI},
				Scopes:                        s.oidcConfig.Scopes,
				TokenExpirationSeconds:        testCase.tokenExpirationSeconds,
				RefreshTokenExpirationSeconds: testCase.refreshTokenExpirationSeconds,
			})
			assert.Error(t, err, "Expiration below the schema minimum must be rejected for case %s", testCase.name)
			assert.True(t, k8serrors.IsInvalid(err),
				"Case %s must be rejected as a schema validation error, got %v", testCase.name, err)
		})
	}
}

func (s *OIDCTestSuite) TestOIDCClientStatusReportsLastFiveSecretCharacters() {
	logrus.Infof("Verifying status.clientSecrets on OIDCClient %s reports the tail of the generated secret", s.oidcClientName)

	oidcClientObject, err := s.client.WranglerContext.Mgmt.OIDCClient().Get(s.oidcClientName, metav1.GetOptions{})
	require.NoError(s.T(), err, "Failed to read back OIDCClient %s", s.oidcClientName)

	secretStatus, found := oidcClientObject.Status.ClientSecrets[s.secretKeyName]
	require.True(s.T(), found, "status.clientSecrets must contain the key %s reported at creation", s.secretKeyName)
	require.Len(s.T(), secretStatus.LastFiveCharacters, 5, "lastFiveCharacters must hold exactly five characters")

	require.Equal(s.T(), s.clientSecret[len(s.clientSecret)-5:], secretStatus.LastFiveCharacters,
		"lastFiveCharacters must match the tail of the secret stored in namespace %s under key %s",
		oidcclient.OIDCClientSecretNamespace, s.secretKeyName)
}

func (s *OIDCTestSuite) TestAccessTokenAuthenticatesV3UsersAPI() {
	logrus.Info("Verifying the OIDC access token authenticates GET /v3/users")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, s.accessTokenHeader, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"OIDC access token must authenticate /v3/users, got %d: %s", resp.StatusCode, resp.Body)

	var users map[string]interface{}
	require.NoError(s.T(), json.Unmarshal(resp.Body, &users))
	require.Equal(s.T(), "collection", users["type"], "/v3/users must return a collection")
	require.Contains(s.T(), users, "data", "/v3/users collection must contain a data field")
}

func (s *OIDCTestSuite) TestAccessTokenAuthenticatesV3ClustersAPI() {
	logrus.Info("Verifying the OIDC access token authenticates GET /v3/clusters")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.ClustersPath, s.accessTokenHeader, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"OIDC access token must authenticate /v3/clusters, got %d: %s", resp.StatusCode, resp.Body)
}

func (s *OIDCTestSuite) TestAccessTokenJWTContainsRequiredClaims() {
	logrus.Info("Verifying the OIDC access token JWT contains the required claims")

	claims, err := oidcext.DecodeJWTPayload(s.tokenSet.AccessToken)
	require.NoError(s.T(), err, "access_token must be a valid three-part JWT")

	for _, claim := range []string{"sub", "iss", "exp", "iat"} {
		require.Contains(s.T(), claims, claim, "Access token JWT missing required claim %s", claim)
	}
	require.NotEmpty(s.T(), claims["sub"], "Access token JWT sub claim must not be empty")
}

func (s *OIDCTestSuite) TestTokenEndpointPKCEFlowProducesValidTokens() {
	logrus.Info("Verifying the PKCE auth-code flow returns access, id, and refresh tokens")

	tokenSet := s.completeAuthCodeFlow(s.scopes)
	require.NotEmpty(s.T(), tokenSet.IDToken, "id_token must be present when the openid scope is requested")
	require.NotEmpty(s.T(), tokenSet.RefreshToken, "refresh_token must be present when the offline_access scope is requested")
	require.Equal(s.T(), "Bearer", tokenSet.TokenType, "token_type must be Bearer")
}

func (s *OIDCTestSuite) TestTokenEndpointRefreshTokenExchangeWorks() {
	logrus.Info("Verifying the refresh_token grant produces a usable access token")

	require.NotEmpty(s.T(), s.tokenSet.RefreshToken, "refresh_token must have been issued during suite setup")

	refreshedTokenSet, err := s.oidcAPI.RefreshAccessToken(s.tokenSet.RefreshToken, s.clientID, s.clientSecret)
	require.NoError(s.T(), err, "refresh_token grant must succeed")
	require.NotEmpty(s.T(), refreshedTokenSet.AccessToken, "refresh_token grant returned an empty access token")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, "Bearer "+refreshedTokenSet.AccessToken, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"Refreshed access token must authenticate /v3/users, got %d: %s", resp.StatusCode, resp.Body)
}

func (s *OIDCTestSuite) TestTokenEndpointWrongClientSecretIsRejected() {
	logrus.Info("Verifying the refresh_token grant rejects a wrong client_secret")

	_, err := s.oidcAPI.RefreshAccessToken(s.tokenSet.RefreshToken, s.clientID, namegen.AppendRandomString("wrong-secret"))
	require.Error(s.T(), err, "refresh_token grant must reject a wrong client_secret")
	require.NotContains(s.T(), err.Error(), "500", "A wrong client_secret must not produce a server error")
}

func (s *OIDCTestSuite) TestSecurityMissingAuthHeaderReturns401() {
	logrus.Info("Verifying a request with no Authorization header returns 401")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, "", nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"A missing Authorization header must return 401, got %d: %s", resp.StatusCode, resp.Body)
}

func (s *OIDCTestSuite) TestSecurityMalformedBearerTokenReturns401() {
	logrus.Info("Verifying malformed bearer token formats return 401")

	malformedTokenCases := []struct{ name, header string }{
		{"random-string", "Bearer not-a-jwt-at-all"},
		{"empty-bearer", "Bearer "},
		{"two-part-jwt", "Bearer a.b"},
		{"one-dot", "Bearer a."},
		{"bearer-only", "Bearer"},
		{"spaces-in-token", "Bearer eye . abc . def"},
	}

	for _, testCase := range malformedTokenCases {
		s.T().Run(testCase.name, func(t *testing.T) {
			resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, testCase.header, nil)
			assert.NoError(t, err)
			assert.Equal(t, http.StatusUnauthorized, resp.StatusCode,
				"Malformed bearer token %s must return 401, got %d: %s", testCase.name, resp.StatusCode, resp.Body)
		})
	}
}

func (s *OIDCTestSuite) TestSecurityNonStringKidDoesNotPanic() {
	logrus.Info("Verifying a JWT with a non-string kid header returns 401 without panicking")

	craftedJWT, err := oidcext.CraftJWTWithNonStringKid()
	require.NoError(s.T(), err)

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, "Bearer "+craftedJWT, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"A non-string kid must return 401, got %d: %s", resp.StatusCode, resp.Body)

	responseBody := strings.ToLower(string(resp.Body))
	require.NotContains(s.T(), responseBody, "panic", "A non-string kid must not surface a panic")
	require.NotContains(s.T(), responseBody, "runtime error", "A non-string kid must not surface a runtime error")
}

func (s *OIDCTestSuite) TestSecurityTamperedSignatureReturns401() {
	logrus.Info("Verifying a JWT with a tampered signature returns 401")

	tamperedToken, err := oidcext.TamperJWTSignature(s.tokenSet.AccessToken)
	require.NoError(s.T(), err)

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, "Bearer "+tamperedToken, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode,
		"A tampered signature must return 401, got %d: %s", resp.StatusCode, resp.Body)
}

func (s *OIDCTestSuite) TestRegressionAdminTokenWorksAlongsideOIDCToken() {
	logrus.Info("Verifying the admin token still authenticates while the OIDC access token is in use")

	resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, s.accessTokenHeader, nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode,
		"OIDC access token must authenticate /v3/users, got %d: %s", resp.StatusCode, resp.Body)

	users, err := s.client.Management.User.List(nil)
	require.NoError(s.T(), err, "Admin token must still list users while oidc-provider is enabled")
	require.NotEmpty(s.T(), users.Data, "Admin token returned no users")
}

func (s *OIDCTestSuite) TestUnauthorizedWhenFeatureFlagDisabled() {
	defer func() {
		logrus.Info("Re-enabling the oidc-provider feature flag")

		err := features.UpdateFeatureFlag(s.client, oidcauth.OIDCProviderFeatureFlag, true)
		require.NoError(s.T(), err, "Failed to re-enable oidc-provider feature flag")

		err = deployments.WaitForDeploymentActive(s.client, cluster.LocalCluster, deployments.RancherDeploymentNamespace, deployments.RancherDeploymentName)
		require.NoError(s.T(), err, "Rancher did not become ready after re-enabling oidc-provider")

		s.setSuiteTokenSet()
	}()

	logrus.Info("Disabling the oidc-provider feature flag")

	err := features.UpdateFeatureFlag(s.client, oidcauth.OIDCProviderFeatureFlag, false)
	require.NoError(s.T(), err, "Failed to disable oidc-provider feature flag")

	err = deployments.WaitForDeploymentActive(s.client, cluster.LocalCluster, deployments.RancherDeploymentNamespace, deployments.RancherDeploymentName)
	require.NoError(s.T(), err, "Rancher did not become ready after disabling oidc-provider")

	logrus.Info("Verifying a previously valid OIDC access token is rejected while oidc-provider is disabled")
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, false,
		func(ctx context.Context) (bool, error) {
			resp, err := s.oidcAPI.RawRequest(http.MethodGet, oidcext.UsersPath, s.accessTokenHeader, nil)
			if err != nil {
				return false, nil
			}
			return resp.StatusCode == http.StatusUnauthorized, nil
		},
	)
	require.NoError(s.T(), err, "OIDC access token must stop authenticating while oidc-provider is disabled")
}

func TestOIDCProviderSuite(t *testing.T) {
	suite.Run(t, new(OIDCTestSuite))
}

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
	"github.com/rancher/shepherd/pkg/clientbase"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
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

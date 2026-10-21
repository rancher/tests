//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package metaproxy

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/cloudcredentials/digitalocean"
	extmetaproxy "github.com/rancher/shepherd/extensions/metaproxy"
	"github.com/rancher/shepherd/pkg/session"
	metaproxyactions "github.com/rancher/tests/actions/kubeapi/metaproxy"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	customTestHeader = "X-Custom-Test"
	customTestValue  = "1"
	probeUserAgent   = "metaproxy-coverage"

	credentialLikeProbeValue = "Bearer not-a-rancher-token"

	strippedContentType        = "application/x-www-form-urlencoded"
	rancherPrefixedContentType = "rancher:" + strippedContentType
)

type MetaProxyHeaderForwardingTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	input        metaproxyactions.Input
	destination  string
	token        string
	bearerCredID string
}

func (m *MetaProxyHeaderForwardingTestSuite) SetupSuite() {
	m.session = session.NewSession()

	client, err := rancher.NewClient("", m.session)
	require.NoError(m.T(), err, "Failed to create Rancher client")
	m.client = client
	m.token = client.RancherConfig.AdminToken
	require.NotEmpty(m.T(), m.token, "Set rancher.adminToken; the suite checks that this token never reaches the echo host")

	m.input = metaproxyactions.LoadInput()
	require.NotEmpty(m.T(), m.input.EchoHost, "Set metaProxyInput.echoHost to a host that echoes the request headers it receives")
	m.destination = metaproxyactions.EchoDestination(m.input)

	logrus.Infof("Allowing the echo host %s on the meta proxy", m.input.EchoHost)
	restoreAllowList, err := metaproxyactions.AllowDomain(m.client, m.input.EchoHost)
	require.NoError(m.T(), err, "Failed to add the echo host to the %s setting", metaproxyactions.WhitelistDomainSetting)
	m.session.RegisterCleanupFunc(restoreAllowList)

	require.NoError(m.T(), metaproxyactions.WaitForAllowList(m.client, m.destination), "The meta proxy should reach the echo host once it is allowed")

	reachable, reason, err := metaproxyactions.VerifyEchoHostReachable(m.client, m.destination)
	require.NoError(m.T(), err, "Failed to read whether the meta proxy reaches the echo host")
	require.True(m.T(), reachable, reason)

	digitalOceanCredential, err := digitalocean.CreateDigitalOceanCloudCredentials(m.client, cloudcredentials.CloudCredential{
		DigitalOceanCredentialConfig: &cloudcredentials.DigitalOceanCredentialConfig{AccessToken: probeAccessToken},
	})
	require.NoError(m.T(), err, "Failed to create the bearer probe cloud credential")
	m.bearerCredID = digitalOceanCredential.Namespace + ":" + digitalOceanCredential.Name
}

func (m *MetaProxyHeaderForwardingTestSuite) TearDownSuite() {
	m.session.Cleanup()
}

func (m *MetaProxyHeaderForwardingTestSuite) TestBenignHeadersAreForwarded() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the meta proxy still forwards headers that carry no Rancher credential")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set("Accept", "application/json")
	header.Set("User-Agent", probeUserAgent)
	header.Set(customTestHeader, customTestValue)
	header.Set("Content-Type", rancherPrefixedContentType)
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.Equal(m.T(), "application/json", echoed["accept"], "Accept should be forwarded")
	require.Equal(m.T(), probeUserAgent, echoed["user-agent"], "User-Agent should be forwarded")
	require.Equal(m.T(), customTestValue, echoed[strings.ToLower(customTestHeader)], "A custom header should be forwarded")
	require.Equal(m.T(), strippedContentType, echoed["content-type"], "The meta proxy should strip the rancher: prefix the dashboard sends")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
}

func (m *MetaProxyHeaderForwardingTestSuite) TestRequestBodyIsForwarded() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the meta proxy still forwards a request body on a signer path")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set("Content-Type", "application/json")
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	body := map[string]string{"probe": customTestValue}

	status, echo, err := metaproxyactions.FetchEchoedRequest(m.client, http.MethodPost, metaproxyactions.Destination(m.input, m.input.PostPath), body, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.Equal(m.T(), "application/json", echo.Headers["content-type"], "Content-Type should be forwarded")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echo.Headers, m.token))
	require.JSONEq(m.T(), `{"probe":"1"}`, echo.Data, "The request body should reach the echo host intact")
}

func (m *MetaProxyHeaderForwardingTestSuite) TestRefusedHeadersAreNotForwarded() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the meta proxy still refuses the headers it has always refused")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set("Impersonate-User", "probe-user")
	header.Set("Impersonate-Group", "probe-group")
	header.Set("CF-Connecting-IP", "127.0.0.1")
	header.Set("CF-Ray", "probe-ray")
	header.Set(extmetaproxy.APIAuthHeader, replacementToken)
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")

	for _, refused := range []string{"impersonate-user", "impersonate-group", "cf-connecting-ip", "cf-ray", "x-api-auth-header", "x-api-cattleauth-header"} {
		require.NotContains(m.T(), echoed, refused, "The %s header should not be forwarded", refused)
	}
}

func (m *MetaProxyHeaderForwardingTestSuite) TestCredentialLikeHeadersAreRefused() {
	m.T().Skip("Known issue: 1140")

	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the meta proxy refuses headers that carry a credential by convention")

	probes := []string{"Proxy-Authorization", "X-Api-Tokens", "X-Forwarded-Authorization", "X-Auth-Token"}

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	for _, probe := range probes {
		header.Set(probe, credentialLikeProbeValue)
	}

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token), "The caller's own Rancher token should not reach the echo host in any header")

	for _, probe := range probes {
		require.NotContains(m.T(), echoed, strings.ToLower(probe), "The %s header should not reach the third-party host", probe)
	}
}

func TestMetaProxyHeaderForwardingSuite(t *testing.T) {
	suite.Run(t, new(MetaProxyHeaderForwardingTestSuite))
}

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
	blockedDomain           = "forums.rancher.com"
	unrelatedEndpointDomain = "docs.rancher.com"

	customAllowListHeader = "X-Allowlist-Probe"
	customAllowListValue  = "reached-the-host"
)

type MetaProxyAllowListTestSuite struct {
	suite.Suite
	session             *session.Session
	client              *rancher.Client
	input               metaproxyactions.Input
	destination         string
	token               string
	bearerCredID        string
	proxyEndpointServed bool
}

func (m *MetaProxyAllowListTestSuite) SetupSuite() {
	m.session = session.NewSession()

	client, err := rancher.NewClient("", m.session)
	require.NoError(m.T(), err, "Failed to create Rancher client")
	m.client = client
	m.token = client.RancherConfig.AdminToken
	require.NotEmpty(m.T(), m.token, "Set rancher.adminToken; the suite checks that this token never reaches the echo host")

	m.input = metaproxyactions.LoadInput()
	require.NotEmpty(m.T(), m.input.EchoHost, "Set metaProxyInput.echoHost to a host that echoes the request headers it receives")
	m.destination = metaproxyactions.EchoDestination(m.input)

	digitalOceanCredential, err := digitalocean.CreateDigitalOceanCloudCredentials(m.client, cloudcredentials.CloudCredential{
		DigitalOceanCredentialConfig: &cloudcredentials.DigitalOceanCredentialConfig{AccessToken: probeAccessToken},
	})
	require.NoError(m.T(), err, "Failed to create the bearer probe cloud credential")
	m.bearerCredID = digitalOceanCredential.Namespace + ":" + digitalOceanCredential.Name

	logrus.Info("Checking whether this Rancher server serves the ProxyEndpoint resource")
	m.proxyEndpointServed, err = metaproxyactions.VerifyProxyEndpointServed(m.client)
	require.NoError(m.T(), err, "Failed to read whether this Rancher server serves the ProxyEndpoint resource")
	logrus.Infof("The Rancher server serves the ProxyEndpoint resource: %t", m.proxyEndpointServed)
}

func (m *MetaProxyAllowListTestSuite) TearDownSuite() {
	m.session.Cleanup()
}

func (m *MetaProxyAllowListTestSuite) TestHostNotOnAllowListIsRefused() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the meta proxy refuses %s while it is not on the allow list", m.input.EchoHost)

	restoreAllowList, err := metaproxyactions.SetAllowList(m.client, blockedDomain)
	require.NoError(m.T(), err, "Failed to take the echo host off the %s setting", metaproxyactions.WhitelistDomainSetting)
	subSession.RegisterCleanupFunc(restoreAllowList)

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	resp, err := extmetaproxy.Request(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.NotEqual(m.T(), http.StatusOK, resp.StatusCode, "The meta proxy should refuse a host that is not on the allow list")
	require.False(m.T(), strings.Contains(string(resp.Body), m.token), "The refusal should carry no Rancher credential")

	status, echo, err := metaproxyactions.FetchEchoedRequest(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.NotEqual(m.T(), http.StatusOK, status, "The meta proxy should refuse a host that is not on the allow list")
	require.Nil(m.T(), echo, "No answer from the refused host should reach the client")
}

func (m *MetaProxyAllowListTestSuite) TestAllowListedHostIsUnaffected() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the meta proxy still reaches %s while it is on the allow list", m.input.EchoHost)

	restoreAllowList, err := metaproxyactions.AllowDomain(m.client, m.input.EchoHost)
	require.NoError(m.T(), err, "Failed to add the echo host to the %s setting", metaproxyactions.WhitelistDomainSetting)
	subSession.RegisterCleanupFunc(restoreAllowList)

	require.NoError(m.T(), metaproxyactions.WaitForAllowList(m.client, m.destination), "The meta proxy should reach the echo host once it is allowed")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set(customAllowListHeader, customAllowListValue)

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach a host that is on the allow list")
	require.Equal(m.T(), customAllowListValue, echoed[strings.ToLower(customAllowListHeader)], "An allowed destination should still receive the request unchanged")
}

func (m *MetaProxyAllowListTestSuite) TestHostOutsideEveryProxyEndpointIsRefused() {
	if !m.proxyEndpointServed {
		m.T().Skip("This Rancher server does not serve the ProxyEndpoint resource")
	}

	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a host no ProxyEndpoint covers is refused even while another ProxyEndpoint exists")

	restoreAllowList, err := metaproxyactions.SetAllowList(m.client, blockedDomain)
	require.NoError(m.T(), err, "Failed to take the echo host off the %s setting", metaproxyactions.WhitelistDomainSetting)
	subSession.RegisterCleanupFunc(restoreAllowList)

	_, err = metaproxyactions.CreateProxyEndpoint(m.client, subSession, unrelatedEndpointDomain)
	require.NoError(m.T(), err, "Failed to create a ProxyEndpoint for an unrelated domain")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))

	status, echo, err := metaproxyactions.FetchEchoedRequest(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.NotEqual(m.T(), http.StatusOK, status, "A host no ProxyEndpoint covers should be refused")
	require.Nil(m.T(), echo, "No answer from the refused host should reach the client")
}

func (m *MetaProxyAllowListTestSuite) TestProxyEndpointHostGetsTheSameStripping() {
	if !m.proxyEndpointServed {
		m.T().Skip("This Rancher server does not serve the ProxyEndpoint resource")
	}

	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a host allowed only by a ProxyEndpoint gets the same credential stripping")

	restoreAllowList, err := metaproxyactions.SetAllowList(m.client, blockedDomain)
	require.NoError(m.T(), err, "Failed to take the echo host off the %s setting", metaproxyactions.WhitelistDomainSetting)
	subSession.RegisterCleanupFunc(restoreAllowList)

	_, err = metaproxyactions.CreateProxyEndpoint(m.client, subSession, m.input.EchoHost)
	require.NoError(m.T(), err, "Failed to create a ProxyEndpoint for the echo host")

	require.NoError(m.T(), metaproxyactions.WaitForAllowList(m.client, m.destination), "The meta proxy should reach the echo host once the ProxyEndpoint allows it")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach a host the ProxyEndpoint allows")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), fmt.Sprintf("Bearer %s", probeAccessToken), echoed["authorization"], "The bearer signer should inject the credential secret")

	logrus.Info("Verifying a Rancher token is not forwarded to a ProxyEndpoint host either")

	tokenHeader := http.Header{}
	tokenHeader.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))

	status, echoed, err = metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, tokenHeader)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach a host the ProxyEndpoint allows")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
}

func (m *MetaProxyAllowListTestSuite) TestEveryDisallowedDestinationFormIsRefused() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying every form of a destination that is not on the allow list is refused")

	restoreAllowList, err := metaproxyactions.SetAllowList(m.client, blockedDomain)
	require.NoError(m.T(), err, "Failed to take the echo host off the %s setting", metaproxyactions.WhitelistDomainSetting)
	subSession.RegisterCleanupFunc(restoreAllowList)

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s", rancherSessionCookie, m.token))

	destinations := []string{
		m.destination,
		fmt.Sprintf("https:/%s/%s", m.input.EchoHost, m.input.EchoPath),
		fmt.Sprintf("http:/%s/%s", m.input.EchoHost, m.input.EchoPath),
		fmt.Sprintf("%s:443/%s", m.input.EchoHost, m.input.EchoPath),
	}

	for _, destination := range destinations {
		status, echo, err := metaproxyactions.FetchEchoedRequest(m.client, http.MethodGet, destination, nil, header)
		require.NoError(m.T(), err)
		require.NotEqual(m.T(), http.StatusOK, status, "The meta proxy should refuse %s", destination)
		require.Nil(m.T(), echo, "No answer should reach the client from %s", destination)

		resp, err := extmetaproxy.Request(m.client, http.MethodGet, destination, nil, header)
		require.NoError(m.T(), err)
		require.False(m.T(), strings.Contains(string(resp.Body), m.token), "The refusal of %s should carry no Rancher credential", destination)
	}
}

func TestMetaProxyAllowListSuite(t *testing.T) {
	suite.Run(t, new(MetaProxyAllowListTestSuite))
}

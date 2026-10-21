//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package metaproxy

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/cloudcredentials/aws"
	"github.com/rancher/shepherd/extensions/cloudcredentials/digitalocean"
	"github.com/rancher/shepherd/extensions/cloudcredentials/vsphere"
	extmetaproxy "github.com/rancher/shepherd/extensions/metaproxy"
	"github.com/rancher/shepherd/pkg/session"
	metaproxyactions "github.com/rancher/tests/actions/kubeapi/metaproxy"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	probeAccessToken = "fake-digitalocean-token"
	probeUsername    = "probe-user"
	probePassword    = "fake-basic-password"

	probeAccessKey   = "FAKEACCESSKEY"
	probeSecretKey   = "fake-secret-key"
	probeRegion      = "us-east-1"
	probeVcenter     = "probe.vcenter.invalid"
	probeVcenterPort = "443"

	rancherSessionCookie  = "R_SESS"
	clientCookie          = "mine=1"
	awsSignatureAlgorithm = "AWS4-HMAC-SHA256"
)

type MetaProxyCookieTestSuite struct {
	suite.Suite
	session             *session.Session
	client              *rancher.Client
	input               metaproxyactions.Input
	destination         string
	token               string
	bearerCredID        string
	basicCredID         string
	awsCredID           string
	abortsOnSignerError bool
}

func (m *MetaProxyCookieTestSuite) SetupSuite() {
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

	vsphereCredential, err := vsphere.CreateVsphereCloudCredentials(m.client, cloudcredentials.CloudCredential{
		VmwareVsphereConfig: &cloudcredentials.VmwarevsphereCredentialConfig{
			Username:    probeUsername,
			Password:    probePassword,
			Vcenter:     probeVcenter,
			VcenterPort: probeVcenterPort,
		},
	})
	require.NoError(m.T(), err, "Failed to create the basic probe cloud credential")
	m.basicCredID = vsphereCredential.Namespace + ":" + vsphereCredential.Name

	amazonCredential, err := aws.CreateAWSCloudCredentials(m.client, cloudcredentials.CloudCredential{
		AmazonEC2CredentialConfig: &cloudcredentials.AmazonEC2CredentialConfig{
			AccessKey:     probeAccessKey,
			SecretKey:     probeSecretKey,
			DefaultRegion: probeRegion,
		},
	})
	require.NoError(m.T(), err, "Failed to create the awsv4 probe cloud credential")
	m.awsCredID = amazonCredential.Namespace + ":" + amazonCredential.Name

	logrus.Info("Checking whether this Rancher server answers the request itself when a signer cannot resolve its credential")
	m.abortsOnSignerError, err = metaproxyactions.VerifyProxyAbortsOnSignerError(m.client, m.destination)
	require.NoError(m.T(), err, "Failed to read how this Rancher server handles a signer error")
	logrus.Infof("The Rancher server refuses the request when a signer errors: %t", m.abortsOnSignerError)
}

func (m *MetaProxyCookieTestSuite) TearDownSuite() {
	m.session.Cleanup()
}

func (m *MetaProxyCookieTestSuite) TestSessionCookieIsNotForwardedOnBearerSignerPath() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the bearer signer path forwards the credential and drops the Rancher session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), fmt.Sprintf("Bearer %s", probeAccessToken), echoed["authorization"], "The bearer signer should inject the credential secret")
}

func (m *MetaProxyCookieTestSuite) TestSessionCookieIsNotForwardedOnBasicSignerPath() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the basic signer path forwards the credential and drops the Rancher session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BasicSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.basicCredID,
		metaproxyactions.UsernameFieldParam: metaproxyactions.UsernameField,
		metaproxyactions.PasswordFieldParam: metaproxyactions.PasswordField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.True(m.T(), strings.HasPrefix(echoed["authorization"], "Basic "), "The basic signer should inject a basic credential")
}

func (m *MetaProxyCookieTestSuite) TestSessionCookieIsNotForwardedOnDigestSignerPath() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the digest signer path leaks nothing when the echo host offers no digest challenge")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.DigestSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.basicCredID,
		metaproxyactions.UsernameFieldParam: metaproxyactions.UsernameField,
		metaproxyactions.PasswordFieldParam: metaproxyactions.PasswordField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)

	if m.abortsOnSignerError {
		require.NotEqual(m.T(), http.StatusOK, status, "The Rancher server should refuse the request when the digest handshake cannot complete")
		require.Nil(m.T(), echoed, "No request should reach the echo host when the digest handshake cannot complete")

		return
	}

	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
}

func (m *MetaProxyCookieTestSuite) TestDigestSignerStillCompletesItsHandshake() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the digest signer still completes its handshake after the cookie and header changes")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.DigestSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.basicCredID,
		metaproxyactions.UsernameFieldParam: metaproxyactions.UsernameField,
		metaproxyactions.PasswordFieldParam: metaproxyactions.PasswordField,
	}))

	resp, err := extmetaproxy.Request(m.client, http.MethodGet, metaproxyactions.DigestDestination(m.input, probeUsername, probePassword), nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, resp.StatusCode, "The digest signer should still authenticate against the digest endpoint")
}

func (m *MetaProxyCookieTestSuite) TestSessionCookieIsNotForwardedOnAWSV4SignerPath() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the awsv4 signer path signs the request and drops the Rancher session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.AWSV4Signer, map[string]string{
		metaproxyactions.CredIDParam: m.awsCredID,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.True(m.T(), strings.HasPrefix(echoed["authorization"], awsSignatureAlgorithm), "The awsv4 signer should sign the outgoing request")
	require.Contains(m.T(), echoed, "x-amz-date", "The awsv4 signer should set the signing date")
}

func (m *MetaProxyCookieTestSuite) TestSessionCookieIsNotForwardedOnArbitrarySignerPath() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the arbitrary signer path sets the headers it names and drops the Rancher session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.ArbitrarySigner, map[string]string{
		"headers": "X-Arbitrary-Probe=set-by-signer",
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), "set-by-signer", echoed["x-arbitrary-probe"], "The arbitrary signer should set the header it names")
	require.NotContains(m.T(), echoed, "authorization", "The arbitrary signer should leave no Authorization behind")
}

func (m *MetaProxyCookieTestSuite) TestClientCookieReplacesSessionCookieOnSignerPath() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying X-Api-Cookie-Header replaces the Rancher session cookie on a signer path")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.APICookieHeader, clientCookie)
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.Equal(m.T(), clientCookie, echoed["cookie"], "The client cookie should be the only cookie forwarded")
	require.NotContains(m.T(), echoed["cookie"], rancherSessionCookie, "The Rancher session cookie should not be forwarded")
	require.NotContains(m.T(), echoed, strings.ToLower(extmetaproxy.APICookieHeader), "X-Api-Cookie-Header should not be forwarded")
	require.Equal(m.T(), fmt.Sprintf("Bearer %s", probeAccessToken), echoed["authorization"], "The bearer signer should inject the credential secret")
}

func (m *MetaProxyCookieTestSuite) TestSessionCookieIsNotForwardedWithoutSigner() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the no-signer path drops the Rancher session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.NotContains(m.T(), echoed, "authorization", "The no-signer path should forward no Authorization")
}

func (m *MetaProxyCookieTestSuite) TestClientCookieReplacesSessionCookieWithoutSigner() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying X-Api-Cookie-Header replaces the Rancher session cookie on the no-signer path")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.APICookieHeader, clientCookie)

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.Equal(m.T(), clientCookie, echoed["cookie"], "The client cookie should be the only cookie forwarded")
	require.NotContains(m.T(), echoed, strings.ToLower(extmetaproxy.APICookieHeader), "X-Api-Cookie-Header should not be forwarded")
}

func (m *MetaProxyCookieTestSuite) TestUnknownSignerForwardsHeaderVerbatimWithoutCookie() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying an unrecognised signer word forwards the header verbatim and drops the Rancher session cookie")

	unknownSigner := fmt.Sprintf("foo credID=%s", m.bearerCredID)

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, unknownSigner)

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), unknownSigner, echoed["authorization"], "An unrecognised signer word should be forwarded verbatim")
}

func (m *MetaProxyCookieTestSuite) TestEveryRancherCookieIsDropped() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying every cookie on the Rancher origin is dropped on a signer path")

	header := http.Header{}
	header.Add(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; CSRF=abcdef", rancherSessionCookie, m.token))
	header.Add(extmetaproxy.CookieHeader, "R_PCS=1; R_LOCALE=en-us")
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))

	for _, cookie := range []string{rancherSessionCookie, "CSRF", "R_PCS", "R_LOCALE"} {
		for name, value := range echoed {
			require.NotContains(m.T(), value, cookie, "The %s cookie should not reach the echo host in the %s header", cookie, name)
		}
	}
}

func (m *MetaProxyCookieTestSuite) TestDashboardRequestShapeForwardsNoSessionCookie() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the shape the dashboard sends forwards the credential and no Rancher session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s", rancherSessionCookie, m.token))
	header.Set("Accept", "application/json")
	header.Set("X-Requested-With", "XMLHttpRequest")
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), fmt.Sprintf("Bearer %s", probeAccessToken), echoed["authorization"], "The bearer signer should inject the credential secret")
}

func TestMetaProxyCookieSuite(t *testing.T) {
	suite.Run(t, new(MetaProxyCookieTestSuite))
}

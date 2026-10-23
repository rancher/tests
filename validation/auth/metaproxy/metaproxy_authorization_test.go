//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package metaproxy

import (
	"encoding/base64"
	"fmt"
	"net/http"
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
	replacementToken = "Bearer plain-token"
	missingField     = "nope"

	browserCSRFValue    = "03bfd93928adbb6a9fb9eab15bb8c936"
	browserInvocationID = "7fd02328-75fc-4aa1-b479-8908e1538287"
	browserAWSUserAgent = "aws-sdk-js/3.1041.0 ua/2.1 os/macOS lang/js md/browser api/ec2#3.1041.0"
	browserSignature    = "AWS4-HMAC-SHA256 Credential=FAKEACCESSKEY/20261020/us-east-2/ec2/aws4_request, SignedHeaders=host;x-amz-date, Signature=0000000000000000000000000000000000000000000000000000000000000000"
)

type MetaProxyAuthorizationTestSuite struct {
	suite.Suite
	session             *session.Session
	client              *rancher.Client
	destination         string
	token               string
	bearerCredID        string
	abortsOnSignerError bool
}

func (m *MetaProxyAuthorizationTestSuite) SetupSuite() {
	m.session = session.NewSession()

	client, err := rancher.NewClient("", m.session)
	require.NoError(m.T(), err, "Failed to create Rancher client")
	m.client = client
	m.token = client.RancherConfig.AdminToken
	require.NotEmpty(m.T(), m.token, "Set rancher.adminToken; the suite checks that this token never reaches the echo host")

	input := metaproxyactions.LoadInput()
	require.NotEmpty(m.T(), input.EchoHost, "Set metaProxyInput.echoHost to a host that echoes the request headers it receives")
	m.destination = metaproxyactions.EchoDestination(input)

	logrus.Infof("Allowing the echo host %s on the meta proxy", input.EchoHost)
	restoreAllowList, err := metaproxyactions.AllowDomain(m.client, input.EchoHost)
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

	logrus.Info("Checking whether this Rancher server answers the request itself when a signer cannot resolve its credential")
	m.abortsOnSignerError, err = metaproxyactions.VerifyProxyAbortsOnSignerError(m.client, m.destination)
	require.NoError(m.T(), err, "Failed to read how this Rancher server handles a signer error")
	logrus.Infof("The Rancher server refuses the request when a signer errors: %t", m.abortsOnSignerError)
}

func (m *MetaProxyAuthorizationTestSuite) TearDownSuite() {
	m.session.Cleanup()
}

func (m *MetaProxyAuthorizationTestSuite) TestBearerTokenIsNotForwardedWithoutSigner() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a Rancher bearer token is not forwarded when no signer header is sent")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.NotContains(m.T(), echoed, "authorization", "The no-signer path should forward no Authorization at all")
}

func (m *MetaProxyAuthorizationTestSuite) TestBasicTokenIsNotForwardedWithoutSigner() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a Rancher token sent as basic auth is not forwarded when no signer header is sent")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Basic %s", base64.URLEncoding.EncodeToString([]byte(m.token))))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token, base64.URLEncoding.EncodeToString([]byte(m.token))))
	require.NotContains(m.T(), echoed, "authorization", "The no-signer path should forward no Authorization at all")
}

func (m *MetaProxyAuthorizationTestSuite) TestCredentialHeaderCaseVariantsAreRefused() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the meta proxy refuses Authorization and Cookie whatever case they are sent in")

	for _, variant := range []struct {
		authorization string
		cookie        string
	}{
		{authorization: "authorization", cookie: "cookie"},
		{authorization: "AUTHORIZATION", cookie: "COOKIE"},
		{authorization: "Authorization", cookie: "Cookie"},
	} {
		header := http.Header{}
		header[variant.authorization] = []string{fmt.Sprintf("Bearer %s", m.token)}
		header[variant.cookie] = []string{fmt.Sprintf("%s=%s", rancherSessionCookie, m.token)}

		status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
		require.NoError(m.T(), err)
		require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host for the %s variant", variant.authorization)
		require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token), "The %s and %s headers should be refused", variant.authorization, variant.cookie)
	}
}

func (m *MetaProxyAuthorizationTestSuite) TestAPIAuthHeaderReplacesAuthorization() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying X-API-Auth-Header still replaces the outgoing Authorization and drops the session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.APIAuthHeader, replacementToken)

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), replacementToken, echoed["authorization"], "X-API-Auth-Header should set the outgoing Authorization")
	require.NotContains(m.T(), echoed, "x-api-auth-header", "X-API-Auth-Header should not be forwarded")
}

func (m *MetaProxyAuthorizationTestSuite) TestBrowserSignedCredentialShapeForwardsNoSessionCookie() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the shape the dashboard sends when it signs a new cloud credential in the browser forwards no session cookie")

	header := http.Header{}
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("R_PCS=light; R_LOCALE=en-us; CSRF=%s; %s=%s", browserCSRFValue, rancherSessionCookie, m.token))
	header.Set(extmetaproxy.APIAuthHeader, browserSignature)
	header.Set("Amz-Sdk-Invocation-Id", browserInvocationID)
	header.Set("X-Amz-User-Agent", browserAWSUserAgent)

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), browserSignature, echoed["authorization"], "The signature the browser computed should become the outgoing Authorization")
	require.Equal(m.T(), browserInvocationID, echoed["amz-sdk-invocation-id"], "The AWS SDK headers the signature covers should be forwarded")
}

func (m *MetaProxyAuthorizationTestSuite) TestSignerOverridesCallerToken() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a working signer replaces the caller's Rancher token with the credential secret")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)
	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.Equal(m.T(), fmt.Sprintf("Bearer %s", probeAccessToken), echoed["authorization"], "The bearer signer should inject the credential secret")
	require.NotContains(m.T(), echoed, "x-api-cattleauth-header", "X-API-CattleAuth-Header should not be forwarded")
}

func (m *MetaProxyAuthorizationTestSuite) TestUnresolvableCredentialDoesNotForwardToken() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a signer naming a credential that does not exist forwards no Rancher credential")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        fmt.Sprintf("%s:cc-doesnotexist", metaproxyactions.CloudCredentialNamespace),
		metaproxyactions.PasswordFieldParam: metaproxyactions.AccessTokenField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)

	if m.abortsOnSignerError {
		require.NotEqual(m.T(), http.StatusOK, status, "The Rancher server should refuse a signer naming a credential it cannot resolve")
		require.Nil(m.T(), echoed, "No request should reach the echo host when the credential cannot be resolved")

		return
	}

	require.Equal(m.T(), http.StatusOK, status, "The meta proxy should reach the echo host")
	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
}

func (m *MetaProxyAuthorizationTestSuite) TestMissingPasswordFieldDoesNotForwardToken() {
	subSession := m.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a signer naming a field the credential does not hold forwards no Rancher credential")

	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", m.token))
	header.Set(extmetaproxy.CookieHeader, fmt.Sprintf("%s=%s; other=1", rancherSessionCookie, m.token))
	header.Set(extmetaproxy.CattleAuthHeader, metaproxyactions.SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		metaproxyactions.CredIDParam:        m.bearerCredID,
		metaproxyactions.PasswordFieldParam: missingField,
	}))

	status, echoed, err := metaproxyactions.FetchEchoedHeaders(m.client, http.MethodGet, m.destination, nil, header)
	require.NoError(m.T(), err)

	if status != http.StatusOK {
		require.Nil(m.T(), echoed, "No request should reach the echo host when the Rancher server refuses it")

		return
	}

	require.NoError(m.T(), metaproxyactions.VerifyNoRancherCredentialsForwarded(echoed, m.token))
	require.NotContains(m.T(), echoed["authorization"], probeAccessToken, "A field the credential does not hold should yield no secret")
}

func TestMetaProxyAuthorizationSuite(t *testing.T) {
	suite.Run(t, new(MetaProxyAuthorizationTestSuite))
}

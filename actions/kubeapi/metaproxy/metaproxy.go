package metaproxy

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	extmetaproxy "github.com/rancher/shepherd/extensions/metaproxy"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ConfigurationFileKey = "metaProxyInput"

	WhitelistDomainSetting   = "whitelist-domain"
	CloudCredentialNamespace = "cattle-global-data"
	EchoProbeHeader          = "X-Metaproxy-Probe"

	AccessTokenField = "accessToken"
	UsernameField    = "username"
	PasswordField    = "password"

	CredIDParam        = "credID"
	UsernameFieldParam = "usernameField"
	PasswordFieldParam = "passwordField"

	defaultEchoPath   = "headers"
	defaultPostPath   = "post"
	defaultDigestPath = "digest-auth/auth"
	defaultEchoScheme = "https"
	httpEchoScheme    = "http"

	unresolvableCredential = "cc-doesnotexist"
)

// Input is the meta proxy section of the test config
type Input struct {
	EchoHost   string `json:"echoHost" yaml:"echoHost"`
	EchoPath   string `json:"echoPath" yaml:"echoPath"`
	EchoScheme string `json:"echoScheme" yaml:"echoScheme"`
	DigestPath string `json:"digestPath" yaml:"digestPath"`
	PostPath   string `json:"postPath" yaml:"postPath"`
}

// LoadInput returns the meta proxy section of the test config with its defaults applied
func LoadInput() Input {
	var input Input
	config.LoadConfig(ConfigurationFileKey, &input)

	if input.EchoPath == "" {
		input.EchoPath = defaultEchoPath
	}
	if input.EchoScheme == "" {
		input.EchoScheme = defaultEchoScheme
	}
	if input.PostPath == "" {
		input.PostPath = defaultPostPath
	}
	if input.DigestPath == "" {
		input.DigestPath = defaultDigestPath
	}

	return input
}

// EchoDestination returns the meta proxy destination that reaches the echo host of the given config
func EchoDestination(input Input) string {
	return Destination(input, input.EchoPath)
}

// DigestDestination returns the meta proxy destination that asks the echo host for a digest challenge for the given credentials
func DigestDestination(input Input, username, password string) string {
	return Destination(input, fmt.Sprintf("%s/%s/%s", strings.Trim(input.DigestPath, "/"), username, password))
}

// Destination returns the meta proxy destination that reaches the given path on the echo host of the given config
func Destination(input Input, hostPath string) string {
	host := strings.TrimSuffix(input.EchoHost, "/")
	trimmed := strings.TrimPrefix(hostPath, "/")

	if strings.EqualFold(input.EchoScheme, httpEchoScheme) {
		return fmt.Sprintf("%s:/%s/%s", httpEchoScheme, host, trimmed)
	}

	return fmt.Sprintf("%s/%s", host, trimmed)
}

// SignerHeader returns an X-API-CattleAuth-Header value naming the given signer and its parameters in a stable order
func SignerHeader(signer string, params map[string]string) string {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	terms := make([]string, 0, len(keys)+1)
	terms = append(terms, signer)
	for _, key := range keys {
		terms = append(terms, fmt.Sprintf("%s=%s", key, params[key]))
	}

	return strings.Join(terms, " ")
}

// EchoedRequest is what an echo host reports about the request the meta proxy forwarded to it
type EchoedRequest struct {
	Headers map[string]string `json:"headers"`
	Data    string            `json:"data"`
}

// FetchEchoedRequest sends a request through the meta proxy and returns the status and what the echo host received, with the header names in lower case
func FetchEchoedRequest(client *rancher.Client, method, destination string, body interface{}, header http.Header) (int, *EchoedRequest, error) {
	resp, err := extmetaproxy.Request(client, method, destination, body, header)
	if err != nil {
		return 0, nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return resp.StatusCode, nil, nil
	}

	echo := &EchoedRequest{}
	if err := json.Unmarshal(resp.Body, echo); err != nil {
		return resp.StatusCode, nil, fmt.Errorf("failed to read what the echo host at %s received: %w", destination, err)
	}
	if echo.Headers == nil {
		return resp.StatusCode, nil, fmt.Errorf("the echo host at %s returned no headers object", destination)
	}

	lowered := map[string]string{}
	for name, value := range echo.Headers {
		lowered[strings.ToLower(name)] = value
	}
	echo.Headers = lowered

	return resp.StatusCode, echo, nil
}

// FetchEchoedHeaders sends a request through the meta proxy and returns the status and the headers the echo host received, keyed in lower case
func FetchEchoedHeaders(client *rancher.Client, method, destination string, body interface{}, header http.Header) (int, map[string]string, error) {
	status, echo, err := FetchEchoedRequest(client, method, destination, body, header)
	if err != nil || echo == nil {
		return status, nil, err
	}

	return status, echo.Headers, nil
}

// VerifyNoRancherCredentialsForwarded returns an error when the headers the echo host received carry a cookie or any of the given Rancher credentials
func VerifyNoRancherCredentialsForwarded(echoed map[string]string, credentials ...string) error {
	if cookie, ok := echoed[strings.ToLower(extmetaproxy.CookieHeader)]; ok {
		return fmt.Errorf("the meta proxy forwarded a %s header holding %d bytes", extmetaproxy.CookieHeader, len(cookie))
	}

	for name, value := range echoed {
		for _, credential := range credentials {
			if credential != "" && strings.Contains(value, credential) {
				return fmt.Errorf("the meta proxy forwarded a Rancher credential in the %s header", name)
			}
		}
	}

	return nil
}

func adminHeader(client *rancher.Client) http.Header {
	header := http.Header{}
	header.Set(extmetaproxy.AuthorizationHeader, fmt.Sprintf("Bearer %s", client.RancherConfig.AdminToken))

	return header
}

// VerifyEchoHostReachable reports whether the meta proxy reaches an echo host that reflects the headers it receives, and why it does not when it cannot
func VerifyEchoHostReachable(client *rancher.Client, destination string) (bool, string, error) {
	marker := namegen.AppendRandomString("probe")

	header := adminHeader(client)
	header.Set(EchoProbeHeader, marker)

	status, echoed, err := FetchEchoedHeaders(client, http.MethodGet, destination, nil, header)
	if err != nil {
		return false, fmt.Sprintf("The echo host at %s did not answer with the headers it received: %v", destination, err), nil
	}
	if status == http.StatusUnauthorized {
		return false, fmt.Sprintf("The Rancher server answered 401 for %s; the meta proxy sits behind authentication and the admin token did not authenticate", destination), nil
	}
	if status != http.StatusOK {
		return false, fmt.Sprintf("The meta proxy answered %d for %s; check that the host is on the allow list and that Rancher can reach it", status, destination), nil
	}
	if echoed[strings.ToLower(EchoProbeHeader)] != marker {
		return false, fmt.Sprintf("The host at %s did not reflect the %s header it was sent, so it cannot show what the meta proxy forwards", destination, EchoProbeHeader), nil
	}

	return true, "", nil
}

// VerifyProxyAbortsOnSignerError reports whether the Rancher server answers the meta proxy request itself when a signer cannot resolve its credential
func VerifyProxyAbortsOnSignerError(client *rancher.Client, destination string) (bool, error) {
	header := adminHeader(client)
	header.Set(extmetaproxy.CattleAuthHeader, SignerHeader(extmetaproxy.BearerSigner, map[string]string{
		CredIDParam:        fmt.Sprintf("%s:%s", CloudCredentialNamespace, unresolvableCredential),
		PasswordFieldParam: AccessTokenField,
	}))

	resp, err := extmetaproxy.Request(client, http.MethodGet, destination, nil, header)
	if err != nil {
		return false, err
	}

	return resp.StatusCode != http.StatusOK, nil
}

// AllowDomain adds the given domain to the whitelist-domain setting and returns a function that restores its previous value
func AllowDomain(client *rancher.Client, domain string) (func() error, error) {
	original, err := extsettingsapi.GetGlobalSettingValue(client, WhitelistDomainSetting)
	if err != nil {
		return nil, err
	}

	restore := func() error {
		return extsettingsapi.UpdateGlobalSetting(client, WhitelistDomainSetting, original)
	}

	for _, allowed := range strings.Split(original, ",") {
		if strings.TrimSpace(allowed) == domain {
			return restore, nil
		}
	}

	updated := domain
	if original != "" {
		updated = fmt.Sprintf("%s,%s", original, domain)
	}

	logrus.Infof("Adding %s to the %s setting", domain, WhitelistDomainSetting)
	if err := extsettingsapi.UpdateGlobalSetting(client, WhitelistDomainSetting, updated); err != nil {
		return nil, err
	}

	return restore, nil
}

// SetAllowList writes the whitelist-domain setting and returns a function that restores its previous value
func SetAllowList(client *rancher.Client, value string) (func() error, error) {
	original, err := extsettingsapi.GetGlobalSettingValue(client, WhitelistDomainSetting)
	if err != nil {
		return nil, err
	}

	restore := func() error {
		return extsettingsapi.UpdateGlobalSetting(client, WhitelistDomainSetting, original)
	}

	logrus.Infof("Setting %s to %q", WhitelistDomainSetting, value)
	if err := extsettingsapi.UpdateGlobalSetting(client, WhitelistDomainSetting, value); err != nil {
		return nil, err
	}

	return restore, nil
}

// VerifyProxyEndpointServed reports whether the Rancher server serves the ProxyEndpoint resource
func VerifyProxyEndpointServed(client *rancher.Client) (bool, error) {
	_, err := client.WranglerContext.Mgmt.ProxyEndpoint().List(metav1.ListOptions{})
	if err == nil {
		return true, nil
	}

	if apierrors.IsNotFound(err) || meta.IsNoMatchError(err) {
		return false, nil
	}

	return false, err
}

// CreateProxyEndpoint creates a ProxyEndpoint that adds the given domain to the meta proxy allow list
func CreateProxyEndpoint(client *rancher.Client, ts *session.Session, domain string) (*v3.ProxyEndpoint, error) {
	endpoint := &v3.ProxyEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("metaproxy-echo"),
		},
		Spec: v3.ProxyEndpointSpec{
			Routes: []v3.ProxyEndpointRoute{{Domain: domain}},
		},
	}

	logrus.Infof("Creating a ProxyEndpoint that allows %s", domain)

	created, err := client.WranglerContext.Mgmt.ProxyEndpoint().Create(endpoint)
	if err != nil {
		return nil, fmt.Errorf("failed to create a ProxyEndpoint allowing %s: %w", domain, err)
	}

	ts.RegisterCleanupFunc(func() error {
		err := client.WranglerContext.Mgmt.ProxyEndpoint().Delete(created.Name, &metav1.DeleteOptions{})
		if apierrors.IsNotFound(err) {
			return nil
		}

		return err
	})

	return created, nil
}

// WaitForAllowList polls the meta proxy until it stops refusing the given destination
func WaitForAllowList(client *rancher.Client, destination string) error {
	return kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.TwoMinuteTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			status, _, err := FetchEchoedHeaders(client, http.MethodGet, destination, nil, adminHeader(client))
			if err != nil {
				return false, nil
			}

			return status == http.StatusOK, nil
		},
	)
}

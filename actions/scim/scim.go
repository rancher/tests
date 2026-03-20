package scim

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"context"
	"time"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/features"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	SCIMFeatureFlag     = "scim"
	SCIMBasePath        = "/v1-scim"
	SCIMSecretNamespace = "cattle-global-data"
	SCIMSecretDataKey   = "token"

	scimSecretKindLabel   = "cattle.io/kind"
	scimSecretKindValue   = "scim-auth-token"
	scimAuthProviderLabel = "authn.management.cattle.io/provider"

	SCIMSchemaUser    = "urn:ietf:params:scim:schemas:core:2.0:User"
	SCIMSchemaGroup   = "urn:ietf:params:scim:schemas:core:2.0:Group"
	SCIMSchemaPatchOp = "urn:ietf:params:scim:api:messages:2.0:PatchOp"
)

// User is the SCIM user payload for Create and Update requests.
type User struct {
	Schemas    []string `json:"schemas"`
	UserName   string   `json:"userName"`
	ExternalID string   `json:"externalId,omitempty"`
	Active     *bool    `json:"active,omitempty"`
}

// Group is the SCIM group payload for Create and Update requests.
type Group struct {
	Schemas     []string `json:"schemas"`
	ID          string   `json:"id,omitempty"`
	DisplayName string   `json:"displayName"`
	ExternalID  string   `json:"externalId,omitempty"`
	Members     []Member `json:"members,omitempty"`
}

// Member is a member reference inside a Group payload.
type Member struct {
	Value string `json:"value"`
}

// PatchOp is the RFC 7644 §3.5.2 PATCH request envelope.
type PatchOp struct {
	Schemas    []string    `json:"schemas"`
	Operations []Operation `json:"Operations"`
}

// Operation is one operation inside a PatchOp request.
type Operation struct {
	Op    string      `json:"op"`
	Path  string      `json:"path,omitempty"`
	Value interface{} `json:"value"`
}

// Response holds the raw HTTP status code and body from a SCIM call.
type Response struct {
	StatusCode int
	Body       []byte
	Header     http.Header
}

// DecodeJSON unmarshals the response body into target.
func (r *Response) DecodeJSON(target interface{}) error {
	return json.Unmarshal(r.Body, target)
}

// IDFromBody extracts the top-level "id" string from a SCIM resource response.
func (r *Response) IDFromBody() (string, error) {
	var m map[string]interface{}
	if err := json.Unmarshal(r.Body, &m); err != nil {
		return "", err
	}
	id, ok := m["id"].(string)
	if !ok || id == "" {
		return "", fmt.Errorf("id not found in response: %s", string(r.Body))
	}
	return id, nil
}

// BoolPtr returns a pointer to a bool — needed for the SCIM active field.
func BoolPtr(b bool) *bool { return &b }

type transport struct {
	rancherHost string
	provider    string
	bearerToken string
	httpClient  *http.Client
}

func newTransport(rancherHost, provider, bearerToken string) *transport {
	tr := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	return &transport{
		rancherHost: rancherHost,
		provider:    provider,
		bearerToken: bearerToken,
		httpClient:  &http.Client{Transport: tr},
	}
}

func (t *transport) do(method, resource, id string, query url.Values, body interface{}) (*Response, error) {
	rawURL := fmt.Sprintf("%s%s/%s/%s", t.rancherHost, SCIMBasePath, t.provider, resource)
	if id != "" {
		rawURL += "/" + id
	}
	if len(query) > 0 {
		rawURL += "?" + query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequest(method, rawURL, bodyReader)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+t.bearerToken)
	req.Header.Set("Content-Type", "application/scim+json")
	req.Header.Set("Accept", "application/scim+json")

	resp, err := t.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &Response{StatusCode: resp.StatusCode, Body: respBody, Header: resp.Header}, nil
}

// Client is the top-level SCIM client.
// Use Users() or Groups() to get a resource-typed client.
type Client struct {
	t *transport
}

// Users returns a resource client scoped to the /Users endpoint.
func (c *Client) Users() *UserClient {
	return &UserClient{t: c.t}
}

// Groups returns a resource client scoped to the /Groups endpoint.
func (c *Client) Groups() *GroupClient {
	return &GroupClient{t: c.t}
}

// Discovery returns a client for SCIM discovery endpoints.
func (c *Client) Discovery() *DiscoveryClient {
	return &DiscoveryClient{t: c.t}
}

// UserClient provides SCIM operations on the Users resource.
type UserClient struct {
	t *transport
}

func (c *UserClient) List(query url.Values) (*Response, error) {
	return c.t.do(http.MethodGet, "Users", "", query, nil)
}

func (c *UserClient) Create(user User) (*Response, error) {
	return c.t.do(http.MethodPost, "Users", "", nil, user)
}

func (c *UserClient) ByID(id string) (*Response, error) {
	return c.t.do(http.MethodGet, "Users", id, nil, nil)
}

func (c *UserClient) Update(id string, user User) (*Response, error) {
	return c.t.do(http.MethodPut, "Users", id, nil, user)
}

func (c *UserClient) Patch(id string, patch PatchOp) (*Response, error) {
	return c.t.do(http.MethodPatch, "Users", id, nil, patch)
}

func (c *UserClient) Delete(id string) (*Response, error) {
	return c.t.do(http.MethodDelete, "Users", id, nil, nil)
}

// GroupClient provides SCIM operations on the Groups resource.
type GroupClient struct {
	t *transport
}

func (c *GroupClient) List(query url.Values) (*Response, error) {
	return c.t.do(http.MethodGet, "Groups", "", query, nil)
}

func (c *GroupClient) Create(group Group) (*Response, error) {
	return c.t.do(http.MethodPost, "Groups", "", nil, group)
}

func (c *GroupClient) ByID(id string) (*Response, error) {
	return c.t.do(http.MethodGet, "Groups", id, nil, nil)
}

func (c *GroupClient) ByIDWithQuery(id string, query url.Values) (*Response, error) {
	return c.t.do(http.MethodGet, "Groups", id, query, nil)
}

func (c *GroupClient) Update(id string, group Group) (*Response, error) {
	return c.t.do(http.MethodPut, "Groups", id, nil, group)
}

func (c *GroupClient) Patch(id string, patch PatchOp) (*Response, error) {
	return c.t.do(http.MethodPatch, "Groups", id, nil, patch)
}

func (c *GroupClient) Delete(id string) (*Response, error) {
	return c.t.do(http.MethodDelete, "Groups", id, nil, nil)
}

// DiscoveryClient provides SCIM discovery endpoint operations.
type DiscoveryClient struct {
	t *transport
}

func (c *DiscoveryClient) ServiceProviderConfig() (*Response, error) {
	return c.t.do(http.MethodGet, "ServiceProviderConfig", "", nil, nil)
}

func (c *DiscoveryClient) ResourceTypes() (*Response, error) {
	return c.t.do(http.MethodGet, "ResourceTypes", "", nil, nil)
}

func (c *DiscoveryClient) ResourceTypeByID(id string) (*Response, error) {
	return c.t.do(http.MethodGet, "ResourceTypes", id, nil, nil)
}

func (c *DiscoveryClient) Schemas() (*Response, error) {
	return c.t.do(http.MethodGet, "Schemas", "", nil, nil)
}

func (c *DiscoveryClient) SchemaByID(id string) (*Response, error) {
	return c.t.do(http.MethodGet, "Schemas", id, nil, nil)
}

func EnableSCIMFeatureFlag(client *rancher.Client) error {
	logrus.Info("Enabling SCIM feature flag")
	return features.UpdateFeatureFlag(client, SCIMFeatureFlag, true)
}

func DisableSCIMFeatureFlag(client *rancher.Client) error {
	logrus.Info("Disabling SCIM feature flag")
	return features.UpdateFeatureFlag(client, SCIMFeatureFlag, false)
}

func IsSCIMFeatureFlagEnabled(client *rancher.Client) (bool, error) {
	return features.IsEnabled(client, SCIMFeatureFlag)
}

func ReenableSCIMFeatureFlag(client *rancher.Client, providerName string) error {
	logrus.Info("Re-enabling SCIM feature flag")
	featureOpts := &types.ListOpts{Filters: map[string]interface{}{"name": SCIMFeatureFlag}}
	featureList, err := client.Management.Feature.List(featureOpts)
	if err != nil {
		return err
	}
	value := true
	for i := range featureList.Data {
		if featureList.Data[i].Value != nil && *featureList.Data[i].Value {
			logrus.Infof("SCIM feature flag is already enabled")
			break
		}
		featureList.Data[i].Value = &value
		_, err = client.Management.Feature.Update(&featureList.Data[i], &featureList.Data[i])
		if err != nil {
			return err
		}
	}

	sc := NewSCIMClientWithToken(
		fmt.Sprintf("https://%s", client.RancherConfig.Host),
		providerName,
		"probe",
	)
	logrus.Info("Waiting for SCIM endpoint to become available after re-enable")
	return kwait.PollUntilContextTimeout(
		context.Background(),
		5*time.Second,
		defaults.FiveMinuteTimeout,
		true,
		func(ctx context.Context) (bool, error) {
			resp, err := sc.Discovery().ServiceProviderConfig()
			if err != nil {
				return false, nil
			}
			return resp.StatusCode == http.StatusUnauthorized, nil
		},
	)
}

func scimTokenLabelSelector(providerName string) labels.Selector {
	return labels.SelectorFromSet(labels.Set{
		scimSecretKindLabel:   scimSecretKindValue,
		scimAuthProviderLabel: providerName,
	})
}

func FetchSCIMBearerToken(client *rancher.Client, providerName string) (string, error) {
	logrus.Infof("Fetching SCIM bearer token from %s by label %s=%s",
		SCIMSecretNamespace, scimAuthProviderLabel, providerName)
	list, err := client.WranglerContext.Core.Secret().List(
		SCIMSecretNamespace,
		metav1.ListOptions{LabelSelector: scimTokenLabelSelector(providerName).String()},
	)
	if err != nil {
		return "", err
	}
	if len(list.Items) == 0 {
		return "", fmt.Errorf("no SCIM token secret found in %s with label %s=%s",
			SCIMSecretNamespace, scimAuthProviderLabel, providerName)
	}
	token, ok := list.Items[0].Data[SCIMSecretDataKey]
	if !ok || len(token) == 0 {
		return "", fmt.Errorf("key %q not found or empty in secret %s/%s",
			SCIMSecretDataKey, SCIMSecretNamespace, list.Items[0].Name)
	}
	logrus.Infof("Found SCIM token in secret %s/%s", SCIMSecretNamespace, list.Items[0].Name)
	return string(token), nil
}

func generateToken() (string, error) {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func CreateSCIMTokenSecret(client *rancher.Client, providerName string) (string, error) {
	token, err := generateToken()
	if err != nil {
		return "", err
	}

	secretName := fmt.Sprintf("scim-token-%s", providerName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: SCIMSecretNamespace,
			Labels: map[string]string{
				scimSecretKindLabel:   scimSecretKindValue,
				scimAuthProviderLabel: providerName,
			},
		},
		Data: map[string][]byte{
			SCIMSecretDataKey: []byte(token),
		},
	}

	logrus.Infof("Creating SCIM token secret %s/%s", SCIMSecretNamespace, secretName)
	_, err = client.WranglerContext.Core.Secret().Create(secret)
	if err != nil {
		return "", err
	}
	return token, nil
}

// NewSCIMClientWithToken constructs a Client using the provided bearer token.
// Used when you already have the token (e.g. for invalid-token tests).
func NewSCIMClientWithToken(rancherHost, providerName, bearerToken string) *Client {
	return &Client{t: newTransport(rancherHost, providerName, bearerToken)}
}

// SetupSCIMClient performs the full SCIM pre-test setup:
//  1. Enable the SCIM feature flag if not already enabled (triggers Rancher restart)
//  2. Enable the auth provider
//  3. Fetch existing SCIM token secret, or create one if none exists
//  4. Return a ready-to-use Client
func SetupSCIMClient(client *rancher.Client, providerName string) (*Client, error) {
	enabled, err := IsSCIMFeatureFlagEnabled(client)
	if err != nil {
		return nil, err
	}

	if !enabled {
		err = EnableSCIMFeatureFlag(client)
		if err != nil {
			return nil, err
		}
	}

	err = auth.EnsureAuthProviderEnabled(client, providerName)
	if err != nil {
		return nil, err
	}

	token, err := FetchSCIMBearerToken(client, providerName)
	if err != nil {
		logrus.Infof("No SCIM token secret found for provider %s, creating one", providerName)
		token, err = CreateSCIMTokenSecret(client, providerName)
		if err != nil {
			return nil, err
		}
	}

	host := fmt.Sprintf("https://%s", client.RancherConfig.Host)
	return NewSCIMClientWithToken(host, providerName, token), nil
}

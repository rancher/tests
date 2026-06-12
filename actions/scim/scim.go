package scim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"sync"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/pkg/clientbase"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/features"
	configmapsapi "github.com/rancher/tests/actions/kubeapi/configmaps"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	SCIMFeatureFlag     = "scim"
	SCIMSecretNamespace = "cattle-global-data"
	SCIMSecretDataKey   = "token"

	scimSecretKindLabel   = "cattle.io/kind"
	scimSecretKindValue   = "scim-auth-token"
	scimAuthProviderLabel = "authn.management.cattle.io/provider"
)

var errNoSCIMTokenSecret = fmt.Errorf("no SCIM token secret found")

// FetchSCIMBearerToken retrieves the SCIM bearer token for the given auth provider from the cattle-global-data namespace
func FetchSCIMBearerToken(client *rancher.Client, providerName string) (string, error) {
	logrus.Infof("Fetching SCIM bearer token from %s by label %s=%s",
		SCIMSecretNamespace, scimAuthProviderLabel, providerName)
	selector := labels.SelectorFromSet(labels.Set{
		scimSecretKindLabel:   scimSecretKindValue,
		scimAuthProviderLabel: providerName,
	})
	list, err := client.WranglerContext.Core.Secret().List(
		SCIMSecretNamespace,
		metav1.ListOptions{LabelSelector: selector.String()},
	)
	if err != nil {
		return "", err
	}
	if len(list.Items) == 0 {
		return "", errNoSCIMTokenSecret
	}
	newest := &list.Items[0]
	for i := 1; i < len(list.Items); i++ {
		if list.Items[i].CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = &list.Items[i]
		}
	}
	if len(list.Items) > 1 {
		logrus.Warnf("Multiple SCIM token secrets found for provider %s, using newest: %s", providerName, newest.Name)
	}
	token, ok := newest.Data[SCIMSecretDataKey]
	if !ok || len(token) == 0 {
		return "", fmt.Errorf("key %q not found or empty in secret %s/%s",
			SCIMSecretDataKey, SCIMSecretNamespace, newest.Name)
	}
	logrus.Infof("Found SCIM token in secret %s/%s", SCIMSecretNamespace, newest.Name)
	return string(token), nil
}

// CreateSCIMTokenSecret generates a new random bearer token and stores it as a Kubernetes secret in the cattle-global-data namespace
func CreateSCIMTokenSecret(client *rancher.Client, providerName string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

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
	_, err := client.WranglerContext.Core.Secret().Create(secret)
	if err != nil {
		return "", err
	}
	return token, nil
}

// SetupSCIMClient ensures the SCIM feature flag is enabled and the auth provider
// is active, then fetches or creates a bearer token secret and returns a configured
// SCIM client ready to make requests against the given provider.
func SetupSCIMClient(client *rancher.Client, providerName string) (*scimclient.Client, error) {
	enabled, err := features.IsEnabled(client, SCIMFeatureFlag)
	if err != nil {
		return nil, err
	}

	if !enabled {
		if err = features.UpdateFeatureFlag(client, SCIMFeatureFlag, true); err != nil {
			return nil, err
		}
	}

	if err = auth.EnsureAuthProviderEnabled(client, providerName); err != nil {
		return nil, err
	}

	token, err := FetchSCIMBearerToken(client, providerName)
	if err != nil {
		if err != errNoSCIMTokenSecret {
			return nil, err
		}
		logrus.Infof("No SCIM token secret found for provider %s, creating one", providerName)
		token, err = CreateSCIMTokenSecret(client, providerName)
		if err != nil {
			return nil, err
		}
	}

	return scimclient.NewClient(&clientbase.ClientOpts{
		URL:      fmt.Sprintf("https://%s", client.RancherConfig.Host),
		TokenKey: token,
		Insecure: true,
	}, providerName), nil
}

// NewSCIMClientWithToken returns a SCIM client configured with the given host, provider, and bearer token
func NewSCIMClientWithToken(host, providerName, token string) *scimclient.Client {
	return scimclient.NewClient(&clientbase.ClientOpts{
		URL:      fmt.Sprintf("https://%s", host),
		TokenKey: token,
		Insecure: true,
	}, providerName)
}

// WaitForSCIMResourceDeletion polls a SCIM GET endpoint until it returns 404.
func WaitForSCIMResourceDeletion(getFunc func() (int, error)) error {
	return kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.OneMinuteTimeout,
		false,
		func(ctx context.Context) (bool, error) {
			status, err := getFunc()
			if err != nil {
				return false, nil
			}
			return status == http.StatusNotFound, nil
		},
	)
}

// CreateSCIMUser creates a SCIM user with a random username and returns the username and ID.
func CreateSCIMUser(scimClient *scimclient.Client, externalID string, active bool) (string, string, error) {
	userName := namegen.AppendRandomString("scim-user")
	user := scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: userName,
	}
	if externalID != "" {
		user.ExternalID = externalID
	}
	if active {
		user.Active = scimclient.BoolPtr(true)
	}
	resp, err := scimClient.Users().Create(user)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("expected 201, got %d: %s", resp.StatusCode, string(resp.Body))
	}
	userID, err := resp.IDFromBody()
	return userName, userID, err
}

// CreateSCIMGroup creates a SCIM group with a random displayName and returns the displayName and ID.
func CreateSCIMGroup(scimClient *scimclient.Client, externalID string) (string, string, error) {
	groupName := namegen.AppendRandomString("scim-group")
	group := scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		DisplayName: groupName,
	}
	if externalID != "" {
		group.ExternalID = externalID
	}
	resp, err := scimClient.Groups().Create(group)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", "", fmt.Errorf("expected 201, got %d: %s", resp.StatusCode, string(resp.Body))
	}
	groupID, err := resp.IDFromBody()
	return groupName, groupID, err
}

// WaitForGroupMemberCount polls GET /Groups/{id} until the members array has
// exactly expectedCount entries, allowing time for the wrangler cache to reflect writes.
func WaitForGroupMemberCount(scimClient *scimclient.Client, groupID string, expectedCount int) error {
	return kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.OneMinuteTimeout,
		false,
		func(ctx context.Context) (bool, error) {
			resp, err := scimClient.Groups().ByID(groupID)
			if err != nil || resp.StatusCode != http.StatusOK {
				return false, nil
			}
			var body map[string]interface{}
			if err := resp.DecodeJSON(&body); err != nil {
				return false, nil
			}
			members, _ := body["members"].([]interface{})
			return len(members) == expectedCount, nil
		},
	)
}

// ListSCIMUsersPage fetches a SCIM user page and returns the decoded JSON body.
func ListSCIMUsersPage(scimClient *scimclient.Client, start, count int) (map[string]interface{}, error) {
	params := url.Values{}
	params.Set("startIndex", fmt.Sprintf("%d", start))
	params.Set("count", fmt.Sprintf("%d", count))
	resp, err := scimClient.Users().List(params)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("expected 200, got %d: %s", resp.StatusCode, string(resp.Body))
	}
	var body map[string]interface{}
	if err := resp.DecodeJSON(&body); err != nil {
		return nil, err
	}
	return body, nil
}

// CheckStatus returns an error if the response status code does not match expected,
// including the response body in the error message for diagnostics.
func CheckStatus(resp *scimclient.Response, expected int, msg string) error {
	if resp.StatusCode != expected {
		return fmt.Errorf("%s: expected %d, got %d: %s", msg, expected, resp.StatusCode, string(resp.Body))
	}
	return nil
}

// SCIMConfigMapName returns the per-provider SCIM ConfigMap name in cattle-global-data.
func SCIMConfigMapName(providerName string) string {
	return fmt.Sprintf("scim-config-%s", providerName)
}

// BaselineSCIMConfigMap returns the canonical baseline ConfigMap data for SCIM tests:
// SCIM enabled, all other keys at defaults. Returns a fresh map each call.
func BaselineSCIMConfigMap() map[string]string {
	return map[string]string{"enabled": "true"}
}

// RateLimitSCIMConfigMap returns a SCIM ConfigMap with rate-limit keys set on top of
// the baseline (enabled=true). Use rps=0 to express disabled.
func RateLimitSCIMConfigMap(rps, burst int) map[string]string {
	return map[string]string{
		"enabled":                    "true",
		"rateLimitRequestsPerSecond": strconv.Itoa(rps),
		"rateLimitBurst":             strconv.Itoa(burst),
	}
}

// RestoreSCIMBaseline restores the SCIM ConfigMap to the canonical baseline state
// (enabled=true, no other keys) and waits for the endpoint to return 200. Intended
// for use in test cleanup.
func RestoreSCIMBaseline(client *rancher.Client, scimClient *scimclient.Client, providerName string) error {
	if err := SetSCIMConfigMap(client, providerName, BaselineSCIMConfigMap()); err != nil {
		return err
	}
	return WaitForSCIMEndpointStatus(scimClient, http.StatusOK)
}

// SetSCIMConfigMap creates or updates the scim-config-<provider> ConfigMap in
// cattle-global-data with the given data. Idempotent: creates if missing, updates if present.
func SetSCIMConfigMap(client *rancher.Client, providerName string, data map[string]string) error {
	name := SCIMConfigMapName(providerName)
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, extclusterapi.LocalCluster)
	if err != nil {
		return fmt.Errorf("failed to get local cluster context: %w", err)
	}

	existing, err := clusterContext.Core.ConfigMap().Get(SCIMSecretNamespace, name, metav1.GetOptions{})
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("failed to get configmap %s/%s: %w", SCIMSecretNamespace, name, err)
		}
		logrus.Infof("Creating SCIM ConfigMap %s/%s with %d keys", SCIMSecretNamespace, name, len(data))
		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      name,
				Namespace: SCIMSecretNamespace,
			},
			Data: data,
		}
		_, err = clusterContext.Core.ConfigMap().Create(cm)
		return err
	}

	logrus.Infof("Updating SCIM ConfigMap %s/%s with %d keys", SCIMSecretNamespace, name, len(data))
	_, err = configmapsapi.UpdateConfigMap(client, extclusterapi.LocalCluster, SCIMSecretNamespace, existing.Name, data)
	return err
}

// DeleteSCIMConfigMap removes the scim-config-<provider> ConfigMap from cattle-global-data.
// Returns nil if the ConfigMap is already absent.
func DeleteSCIMConfigMap(client *rancher.Client, providerName string) error {
	name := SCIMConfigMapName(providerName)
	logrus.Infof("Deleting SCIM ConfigMap %s/%s", SCIMSecretNamespace, name)
	err := configmapsapi.DeleteConfigMap(client, extclusterapi.LocalCluster, SCIMSecretNamespace, name)
	if err != nil && apierrors.IsNotFound(err) {
		return nil
	}
	return err
}

// WaitForSCIMEndpointStatus polls GET /Users on the given SCIM client until the
// response status code matches expectedStatus or the timeout elapses.
func WaitForSCIMEndpointStatus(scimClient *scimclient.Client, expectedStatus int) error {
	return kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.FiveMinuteTimeout,
		false,
		func(ctx context.Context) (bool, error) {
			resp, err := scimClient.Users().List(nil)
			if err != nil {
				return false, nil
			}
			return resp.StatusCode == expectedStatus, nil
		},
	)
}

// BurstSCIMRequests fires n concurrent GET /Users requests and returns the per-request
// status codes. Order is not guaranteed. Used for rate-limit testing.
func BurstSCIMRequests(scimClient *scimclient.Client, n int) ([]int, error) {
	if n <= 0 {
		return nil, fmt.Errorf("BurstSCIMRequests: n must be > 0, got %d", n)
	}
	results := make([]int, n)
	errs := make([]error, n)
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(idx int) {
			defer wg.Done()
			resp, err := scimClient.Users().List(nil)
			if err != nil {
				errs[idx] = err
				return
			}
			results[idx] = resp.StatusCode
		}(i)
	}
	wg.Wait()
	for _, err := range errs {
		if err != nil {
			return results, err
		}
	}
	return results, nil
}

// ValidateSCIMErrorBody decodes the response body and asserts it matches the SCIM
// error schema (schemas, status as string, detail). expectedStatus is compared
// against the status field as a string.
func ValidateSCIMErrorBody(resp *scimclient.Response, expectedStatus int) error {
	var body map[string]interface{}
	if err := resp.DecodeJSON(&body); err != nil {
		return fmt.Errorf("error body is not valid JSON: %w (body: %s)", err, string(resp.Body))
	}
	schemas, ok := body["schemas"].([]interface{})
	if !ok || len(schemas) == 0 {
		return fmt.Errorf("error body missing schemas array: %s", string(resp.Body))
	}
	schemaURN, _ := schemas[0].(string)
	if schemaURN != "urn:ietf:params:scim:api:messages:2.0:Error" {
		return fmt.Errorf("error body schemas[0] = %q, want SCIM Error URN", schemaURN)
	}
	status, ok := body["status"].(string)
	if !ok {
		return fmt.Errorf("error body status field is not a string: %s", string(resp.Body))
	}
	if status != strconv.Itoa(expectedStatus) {
		return fmt.Errorf("error body status = %q, want %q", status, strconv.Itoa(expectedStatus))
	}
	detail, _ := body["detail"].(string)
	if detail == "" {
		return fmt.Errorf("error body detail is empty: %s", string(resp.Body))
	}
	return nil
}

// GetRetryAfterSeconds reads the Retry-After header from a SCIM response and parses
// it as an integer number of seconds. Returns an error if the header is missing or non-numeric.
func GetRetryAfterSeconds(resp *scimclient.Response) (int, error) {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0, fmt.Errorf("Retry-After header missing")
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, fmt.Errorf("Retry-After header %q is not a valid integer: %w", v, err)
	}
	return n, nil
}

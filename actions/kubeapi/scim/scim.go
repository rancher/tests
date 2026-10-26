package scim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	configmapsapi "github.com/rancher/shepherd/extensions/kubeapi/configmaps"
	extfeatures "github.com/rancher/shepherd/extensions/kubeapi/features"
	extauthscim "github.com/rancher/shepherd/extensions/kubeapi/scim"
	secretsapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	settingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	"github.com/rancher/shepherd/pkg/clientbase"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/kubeapi/rancherserver"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	SCIMInputConfigKey       = "scimInput"
	SCIMFeatureFlag          = "scim"
	SCIMSecretNamespace      = "cattle-global-data"
	SCIMTokenSecretNamespace = "cattle-system"
	SCIMSecretDataKey        = "token"
	SCIMTokenExpirySetting   = "expire-scim-tokens-after"

	SCIMConfigEnabledKey          = "enabled"
	SCIMConfigPausedKey           = "paused"
	SCIMConfigUserIDAttributeKey  = "userIdAttribute"
	SCIMConfigGroupIDAttributeKey = "groupIdAttribute"
	SCIMConfigExternalIDValue     = "externalId"
	SCIMConfigUserNameValue       = "userName"
	SCIMConfigTrue                = "true"
	SCIMConfigFalse               = "false"

	SCIMOperationGet    = "GET"
	SCIMOperationUpdate = "PUT"
	SCIMOperationPatch  = "PATCH"
	SCIMOperationDelete = "DELETE"

	SCIMFeatureDescription = "Enable SCIM identity provisioning"
	SCIMExternalIDPrefix   = "scim-ext"
	SCIMPathPrefix         = "/v1-scim/"

	SCIMAuditUserPrefix   = "system:scim:"
	SCIMAuditGroup        = "system:scim"
	SCIMAuditProviderKey  = "scim.cattle.io/provider"
	SCIMAuditTokenKey     = "scim.cattle.io/token-id"
	SCIMAuditRedacted     = "[redacted]"
	SCIMAuditPasswordPath = "password"

	UserAttributeExternalIDKey = "externalid"
	UserAttributeEmailKey      = "email"
	UserAttributeUserNameKey   = "username"

	scimSecretKindLabel     = "cattle.io/kind"
	scimSecretKindValue     = "scim-auth-token"
	scimAuthProviderLabel   = "authn.management.cattle.io/provider"
	scimCreateMaxAttempts   = 3
	scimPatchOpReplace      = "replace"
	scimPatchPathActive     = "active"
	scimListPageSize        = 100
	scimProbeProvider       = "azuread"
	scimProbeRefusalsNeeded = 3
)

var errNoSCIMTokenSecret = fmt.Errorf("no SCIM token secret found")

var (
	tokenNamespaceMu    sync.Mutex
	tokenNamespaceCache = map[string]string{}
)

// SCIMInput is the SCIM section of the test config
type SCIMInput struct {
	BuildType string `yaml:"buildType" json:"buildType"`
}

// FetchSCIMBearerToken retrieves the SCIM bearer token for the given auth provider from the namespace the Rancher server reads it from
func FetchSCIMBearerToken(client *rancher.Client, providerName string) (string, error) {
	namespace, err := ResolveSCIMTokenNamespace(client, providerName)
	if err != nil {
		return "", err
	}

	return FetchSCIMBearerTokenFromNamespace(client, providerName, namespace)
}

// FetchSCIMBearerTokenFromNamespace retrieves the SCIM bearer token for the given auth provider from the given namespace
func FetchSCIMBearerTokenFromNamespace(client *rancher.Client, providerName, namespace string) (string, error) {
	logrus.Infof("Fetching SCIM bearer token from %s by label %s=%s",
		namespace, scimAuthProviderLabel, providerName)

	list, err := ListSCIMTokenSecrets(client, providerName, namespace)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "", errNoSCIMTokenSecret
	}
	newest := &list[0]
	for i := 1; i < len(list); i++ {
		if list[i].CreationTimestamp.After(newest.CreationTimestamp.Time) {
			newest = &list[i]
		}
	}
	if len(list) > 1 {
		logrus.Warnf("Multiple SCIM token secrets found for provider %s, using newest: %s", providerName, newest.Name)
	}
	token, ok := newest.Data[SCIMSecretDataKey]
	if !ok || len(token) == 0 {
		return "", fmt.Errorf("key %q not found or empty in secret %s/%s",
			SCIMSecretDataKey, namespace, newest.Name)
	}
	logrus.Infof("Found SCIM token in secret %s/%s", namespace, newest.Name)
	return string(token), nil
}

// ListSCIMTokenSecrets returns the SCIM token secrets of the given auth provider in the given namespace
func ListSCIMTokenSecrets(client *rancher.Client, providerName, namespace string) ([]corev1.Secret, error) {
	list, err := secretsapi.ListSecrets(client, extclusterapi.LocalCluster, namespace,
		metav1.ListOptions{LabelSelector: scimTokenSelector(providerName).String()})
	if err != nil {
		return nil, err
	}

	return list.Items, nil
}

func scimTokenSelector(providerName string) labels.Selector {
	return labels.SelectorFromSet(labels.Set{
		scimSecretKindLabel:   scimSecretKindValue,
		scimAuthProviderLabel: providerName,
	})
}

// CreateSCIMTokenSecret generates a new random bearer token and stores it in the namespace the Rancher server reads SCIM tokens from
func CreateSCIMTokenSecret(client *rancher.Client, providerName string) (string, error) {
	namespace, err := ResolveSCIMTokenNamespace(client, providerName)
	if err != nil {
		return "", err
	}

	secretName := fmt.Sprintf("scim-token-%s", providerName)
	_, token, err := CreateSCIMTokenSecretInNamespace(client, providerName, namespace, secretName)

	return token, err
}

// ResolveSCIMTokenNamespace returns the namespace the Rancher server reads the given provider's SCIM tokens from
func ResolveSCIMTokenNamespace(client *rancher.Client, providerName string) (string, error) {
	key := client.RancherConfig.Host + "/" + providerName

	tokenNamespaceMu.Lock()
	defer tokenNamespaceMu.Unlock()

	if namespace, known := tokenNamespaceCache[key]; known {
		return namespace, nil
	}

	namespace, err := probeSCIMTokenNamespace(client, providerName)
	if err != nil {
		return "", err
	}

	logrus.Infof("The Rancher server reads SCIM tokens for provider %s from %s", providerName, namespace)
	tokenNamespaceCache[key] = namespace

	return namespace, nil
}

// probeSCIMTokenNamespace plants a token secret in the legacy namespace and reports which namespace the Rancher server honours
func probeSCIMTokenNamespace(client *rancher.Client, providerName string) (string, error) {
	secretName := namegen.AppendRandomString("scim-token-probe")

	_, token, err := CreateSCIMTokenSecretInNamespace(client, providerName, SCIMSecretNamespace, secretName)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := secretsapi.DeleteSecret(client, extclusterapi.LocalCluster, SCIMSecretNamespace, secretName, false); err != nil {
			logrus.WithError(err).Warnf("Failed to delete the SCIM token probe secret %s/%s", SCIMSecretNamespace, secretName)
		}
	}()

	probeClient := NewSCIMClientWithToken(client.RancherConfig.Host, providerName, token)

	namespace := ""
	refusals := 0

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true,
		func(ctx context.Context) (bool, error) {
			resp, listErr := probeClient.Users().List(nil)
			if listErr != nil {
				refusals = 0
				return false, nil
			}

			switch resp.StatusCode {
			case http.StatusOK:
				namespace = SCIMSecretNamespace
				return true, nil
			case http.StatusUnauthorized:
				refusals++
				if refusals < scimProbeRefusalsNeeded {
					return false, nil
				}

				namespace = SCIMTokenSecretNamespace

				return true, nil
			default:
				refusals = 0
				return false, nil
			}
		})
	if pollErr != nil && !kwait.Interrupted(pollErr) {
		return "", pollErr
	}

	if namespace == "" {
		return "", fmt.Errorf("the probe never got a settled answer for which namespace provider %s reads SCIM tokens from", providerName)
	}

	return namespace, nil
}

// FetchSCIMTokenSecretNames returns the names of the given provider's SCIM token secrets across both namespaces a Rancher server may read them from
func FetchSCIMTokenSecretNames(client *rancher.Client, providerName string) ([]string, error) {
	var names []string

	for _, namespace := range []string{SCIMTokenSecretNamespace, SCIMSecretNamespace} {
		list, err := secretsapi.ListSecrets(client, extclusterapi.LocalCluster, namespace,
			metav1.ListOptions{LabelSelector: scimTokenSelector(providerName).String()})
		if err != nil {
			return nil, err
		}

		names = append(names, list.Names()...)
	}

	return names, nil
}

// VerifySCIMTokenNamespaceEnforced reports whether the Rancher server refuses a SCIM token secret that sits only in cattle-global-data
func VerifySCIMTokenNamespaceEnforced(client *rancher.Client, providerName string) (bool, error) {
	namespace, err := ResolveSCIMTokenNamespace(client, providerName)
	if err != nil {
		return false, err
	}

	return namespace == SCIMTokenSecretNamespace, nil
}

// CreateSCIMTokenSecretInNamespace generates a new random bearer token and stores it as a labelled Kubernetes secret of the given name in the given namespace
func CreateSCIMTokenSecretInNamespace(client *rancher.Client, providerName, namespace, secretName string) (*corev1.Secret, string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return nil, "", err
	}
	token := hex.EncodeToString(b)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: namespace,
			Labels: map[string]string{
				scimSecretKindLabel:   scimSecretKindValue,
				scimAuthProviderLabel: providerName,
			},
		},
		Data: map[string][]byte{
			SCIMSecretDataKey: []byte(token),
		},
	}

	logrus.Infof("Creating SCIM token secret %s/%s", namespace, secretName)
	created, err := secretsapi.CreateSecretWithTemplate(client, extclusterapi.LocalCluster, secret)
	if err != nil {
		return nil, "", err
	}

	return created, token, nil
}

// DeleteSCIMTokenSecrets removes the given provider's SCIM token secrets from both namespaces a Rancher server may read them from
func DeleteSCIMTokenSecrets(client *rancher.Client, providerName string) error {
	for _, namespace := range []string{SCIMTokenSecretNamespace, SCIMSecretNamespace} {
		secrets, err := ListSCIMTokenSecrets(client, providerName, namespace)
		if err != nil {
			return err
		}

		for _, secret := range secrets {
			if err := secretsapi.DeleteSecret(client, extclusterapi.LocalCluster, namespace, secret.Name, false); err != nil {
				return err
			}
		}
	}

	return nil
}

// SetSCIMTokenExpiry writes the expire-scim-tokens-after setting and returns a function that restores its previous value
func SetSCIMTokenExpiry(client *rancher.Client, value string) (func() error, error) {
	setting, err := client.WranglerContext.Mgmt.Setting().Get(SCIMTokenExpirySetting, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	previous := setting.Value

	logrus.Infof("Setting %s to %s", SCIMTokenExpirySetting, value)
	if err := settingsapi.UpdateGlobalSetting(client, SCIMTokenExpirySetting, value); err != nil {
		return nil, err
	}

	return func() error {
		logrus.Infof("Restoring %s to %q", SCIMTokenExpirySetting, previous)
		return settingsapi.UpdateGlobalSetting(client, SCIMTokenExpirySetting, previous)
	}, nil
}

// SetupSCIMClient ensures the SCIM feature flag is enabled and the auth provider is active, then fetches or creates a bearer token secret and returns a configured
// SCIM client ready to make requests against the given provider.
func SetupSCIMClient(client *rancher.Client, providerName string) (*scimclient.Client, error) {
	enabled, err := extfeatures.IsFeatureEnabled(client, SCIMFeatureFlag)
	if err != nil {
		return nil, err
	}

	if !enabled {
		if err = extfeatures.UpdateFeatureFlag(client, SCIMFeatureFlag, true); err != nil {
			return nil, err
		}
	}

	if err = auth.EnsureAuthProviderEnabled(client, providerName); err != nil {
		return nil, err
	}

	if err = SetSCIMConfigMap(client, providerName, BaselineSCIMConfigMap()); err != nil {
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

// FetchExpectedBuildType returns the Rancher build type the config says the SCIM suites run against
func FetchExpectedBuildType() string {
	input := new(SCIMInput)
	config.LoadConfig(SCIMInputConfigKey, input)

	return strings.ToLower(input.BuildType)
}

// VerifyExpectedBuildType errors when the Rancher server does not report the build type the config expects
func VerifyExpectedBuildType(client *rancher.Client) error {
	return rancherserver.VerifyBuildType(client, FetchExpectedBuildType())
}

// VerifySCIMServable reports whether the Rancher server serves the SCIM endpoints, and why it does not when it cannot
func VerifySCIMServable(client *rancher.Client) (bool, string, error) {
	feature, err := client.Management.Feature.ByID(SCIMFeatureFlag)
	if err != nil {
		return false, "", err
	}

	if feature.Value != nil && *feature.Value {
		return true, "", nil
	}

	if feature.Status != nil && feature.Status.Prime {
		prime, err := rancherserver.IsRancherPrimeBuild(client)
		if err != nil {
			return false, "", err
		}

		if !prime {
			return false, fmt.Sprintf("The %s feature is Prime only and this Rancher server reports a community build, so the SCIM endpoints answer 404", SCIMFeatureFlag), nil
		}
	}

	return false, fmt.Sprintf("The %s feature is disabled on this Rancher server, so the SCIM endpoints answer 404", SCIMFeatureFlag), nil
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

func createSCIMResourceWithRetry(create func() (*scimclient.Response, error)) (*scimclient.Response, error) {
	var resp *scimclient.Response
	var err error

	attempt := 0
	retryWindow := time.Duration(scimCreateMaxAttempts) * defaults.FiveSecondTimeout

	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, retryWindow, true, func(context.Context) (bool, error) {
		attempt++
		resp, err = create()
		if err == nil {
			return true, nil
		}

		if resp == nil || resp.StatusCode < http.StatusInternalServerError {
			return true, nil
		}

		logrus.Warnf("SCIM create returned %d (attempt %d/%d), retrying in %s: %v", resp.StatusCode, attempt, scimCreateMaxAttempts, defaults.FiveSecondTimeout, err)

		return false, nil
	})
	if pollErr != nil && !kwait.Interrupted(pollErr) {
		return resp, pollErr
	}

	return resp, err
}

// CreateSCIMUser builds a SCIM user with a random username and the given test-specific options
func CreateSCIMUser(scimClient *scimclient.Client, ts *session.Session, externalID string, active bool) (string, string, error) {
	name := namegen.AppendRandomString("scim-user")
	user := scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: name,
	}
	if externalID != "" {
		user.ExternalID = externalID
	}
	if active {
		user.Active = scimclient.BoolPtr(true)
	}
	resp, err := createSCIMResourceWithRetry(func() (*scimclient.Response, error) {
		return extauthscim.CreateUserWithTemplate(scimClient, ts, user)
	})
	if err != nil {
		return "", "", err
	}
	id, err := resp.IDFromBody()
	if err != nil {
		return "", "", err
	}
	return name, id, nil
}

// DeleteSCIMUserByUserName removes every SCIM user carrying the given userName, so a directory identity can be provisioned again
func DeleteSCIMUserByUserName(scimClient *scimclient.Client, userName string) error {
	query := url.Values{}
	query.Set("filter", fmt.Sprintf("userName eq %q", userName))

	resp, err := scimClient.Users().List(query)
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("listing SCIM users named %s answered %d: %s", userName, resp.StatusCode, string(resp.Body))
	}

	body, err := DecodeJSONBody(resp)
	if err != nil {
		return err
	}

	resources, _ := body["Resources"].([]interface{})
	for _, resource := range resources {
		entry, ok := resource.(map[string]interface{})
		if !ok {
			continue
		}

		id, ok := entry["id"].(string)
		if !ok {
			continue
		}

		logrus.Infof("Deleting the SCIM user %s so %s can be provisioned again", id, userName)
		if _, err := scimClient.Users().Delete(id); err != nil {
			return err
		}

		if err := WaitForSCIMResourceDeletion(func() (int, error) {
			getResp, getErr := scimClient.Users().ByID(id)
			if getErr != nil {
				return 0, nil
			}
			return getResp.StatusCode, nil
		}); err != nil {
			return fmt.Errorf("the SCIM user %s was not deleted: %w", id, err)
		}
	}

	return nil
}

// CreateSCIMUserForAuthUser creates an active SCIM user whose userName is the directory name the auth provider builds the user's login principal from
func CreateSCIMUserForAuthUser(scimClient *scimclient.Client, ts *session.Session, distinguishedName, externalID, email string) (string, error) {
	user := scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   distinguishedName,
		ExternalID: externalID,
		Active:     scimclient.BoolPtr(true),
	}
	if email != "" {
		user.Emails = []scimclient.Email{{Value: email, Primary: true}}
	}

	resp, err := createSCIMResourceWithRetry(func() (*scimclient.Response, error) {
		return extauthscim.CreateUserWithTemplate(scimClient, ts, user)
	})
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("POST /Users for %s: expected %d or %d, got %d: %s",
			distinguishedName, http.StatusOK, http.StatusCreated, resp.StatusCode, string(resp.Body))
	}

	return resp.IDFromBody()
}

// ProvisionSCIMUserWithoutAttribute creates an active SCIM user with the given externalID, deletes its
// UserAttribute, and waits until the attribute is absent, returning the SCIM userName and Rancher user
// ID. It reproduces the state of a user provisioned but not yet logged in (no UserAttribute), used to
// exercise the SCIM handlers' missing-attribute recovery path.
func ProvisionSCIMUserWithoutAttribute(client *rancher.Client, scimClient *scimclient.Client, ts *session.Session, externalID string) (string, string, error) {
	userName, userID, err := CreateSCIMUser(scimClient, ts, externalID, true)
	if err != nil {
		return "", "", err
	}

	err = client.WranglerContext.Mgmt.UserAttribute().Delete(userID, &metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return "", "", fmt.Errorf("failed to delete UserAttribute for user %s: %w", userID, err)
	}

	err = WaitForSCIMResourceDeletion(func() (int, error) {
		_, getErr := client.WranglerContext.Mgmt.UserAttribute().Get(userID, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			return http.StatusNotFound, nil
		}
		return http.StatusOK, nil
	})
	if err != nil {
		return "", "", fmt.Errorf("UserAttribute for user %s was not deleted: %w", userID, err)
	}

	return userName, userID, nil
}

// CreateSCIMGroup builds a SCIM group with a random displayName and the given test-specific options
func CreateSCIMGroup(scimClient *scimclient.Client, ts *session.Session, externalID string) (string, string, error) {
	groupName := namegen.AppendRandomString("scim-group")
	group := scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		DisplayName: groupName,
	}
	if externalID != "" {
		group.ExternalID = externalID
	}
	resp, err := createSCIMResourceWithRetry(func() (*scimclient.Response, error) {
		return extauthscim.CreateGroupWithTemplate(scimClient, ts, group)
	})
	if err != nil {
		return "", "", err
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
// (enabled=true, no other keys) and waits for the endpoint to be fully ready
// for both reads and writes. Intended for use in test cleanup.
func RestoreSCIMBaseline(client *rancher.Client, scimClient *scimclient.Client, providerName string) error {
	if err := SetSCIMConfigMap(client, providerName, BaselineSCIMConfigMap()); err != nil {
		return err
	}
	if err := WaitForSCIMEndpointStatus(scimClient, http.StatusOK); err != nil {
		return err
	}
	return WaitForSCIMCreateReady(scimClient)
}

// WaitForSCIMCreateReady polls POST /Users until the SCIM handler accepts creates (201),
// confirming the write path is fully initialized after a ConfigMap change. A probe user
// is created and immediately deleted on success.
func WaitForSCIMCreateReady(scimClient *scimclient.Client) error {
	return pollSCIMCreateReady(scimClient, defaults.OneMinuteTimeout)
}

// WaitForRancherRestart waits for both the SCIM write path and the Kubernetes API proxy
// to be operational after a Rancher restart triggered by a feature flag change.
// Uses a 5-minute timeout for the SCIM probe to accommodate the longer restart cycle.
func WaitForRancherRestart(client *rancher.Client, scimClient *scimclient.Client) error {
	if err := pollSCIMCreateReady(scimClient, defaults.FiveMinuteTimeout); err != nil {
		return err
	}
	return pollWranglerAPIReady(client)
}

func pollSCIMCreateReady(scimClient *scimclient.Client, timeout time.Duration) error {
	// Require two consecutive 201 responses to guard against the handler
	// briefly accepting a request during a transient state after a ConfigMap change.
	consecutiveOK := 0
	lastStatus := 0
	pollErr := kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		timeout,
		false,
		func(ctx context.Context) (bool, error) {
			probe := scimclient.User{
				Schemas:    []string{scimclient.SCIMSchemaUser},
				UserName:   namegen.AppendRandomString("scim-probe"),
				ExternalID: namegen.AppendRandomString("scim-probe-ext"),
			}
			resp, err := scimClient.Users().Create(probe)
			if err != nil || resp == nil {
				consecutiveOK = 0
				return false, nil
			}
			if resp.StatusCode == http.StatusCreated {
				if id, idErr := resp.IDFromBody(); idErr == nil && id != "" {
					scimClient.Users().Delete(id) //nolint:errcheck
				}
				consecutiveOK++
				return consecutiveOK >= 2, nil
			}
			consecutiveOK = 0
			lastStatus = resp.StatusCode

			// A restarting Rancher answers 404 until it registers the SCIM routes, 401 until the token
			// cache syncs and 503 while the provider is still coming up.
			switch {
			case resp.StatusCode >= 500,
				resp.StatusCode == http.StatusNotFound,
				resp.StatusCode == http.StatusUnauthorized:
				return false, nil
			}

			return false, fmt.Errorf("unexpected SCIM create probe status: %d", resp.StatusCode)
		},
	)
	if pollErr != nil {
		return fmt.Errorf("the SCIM write path never became ready, last probe status %d: %w", lastStatus, pollErr)
	}

	return nil
}

func pollWranglerAPIReady(client *rancher.Client) error {
	return kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.TwoMinuteTimeout,
		false,
		func(ctx context.Context) (bool, error) {
			_, err := client.WranglerContext.Core.Secret().List(SCIMSecretNamespace, metav1.ListOptions{})
			return err == nil, nil
		},
	)
}

// SetSCIMConfigMap creates or updates the scim-config-<provider> ConfigMap in
// cattle-global-data with the given data. Idempotent: creates if missing, updates if present.
func SetSCIMConfigMap(client *rancher.Client, providerName string, data map[string]string) error {
	name := fmt.Sprintf("scim-config-%s", providerName)

	existing, err := configmapsapi.GetConfigMapByName(client, extclusterapi.LocalCluster, SCIMSecretNamespace, name)
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
		_, err = configmapsapi.CreateConfigMapWithTemplate(client, extclusterapi.LocalCluster, cm)
		return err
	}

	logrus.Infof("Updating SCIM ConfigMap %s/%s with %d keys", SCIMSecretNamespace, name, len(data))
	existing.Data = data
	_, err = configmapsapi.UpdateConfigMap(client, extclusterapi.LocalCluster, SCIMSecretNamespace, existing)
	return err
}

// DeleteSCIMConfigMap removes the scim-config-<provider> ConfigMap from cattle-global-data.
// Returns nil if the ConfigMap is already absent.
func DeleteSCIMConfigMap(client *rancher.Client, providerName string) error {
	name := fmt.Sprintf("scim-config-%s", providerName)
	logrus.Infof("Deleting SCIM ConfigMap %s/%s", SCIMSecretNamespace, name)
	err := configmapsapi.DeleteConfigMap(client, extclusterapi.LocalCluster, SCIMSecretNamespace, name, true)
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

// WaitForSCIMDiscoveryStatus polls GET /ServiceProviderConfig until it answers the expected status
func WaitForSCIMDiscoveryStatus(scimClient *scimclient.Client, expectedStatus int) error {
	return kwait.PollUntilContextTimeout(
		context.Background(),
		defaults.FiveSecondTimeout,
		defaults.FiveMinuteTimeout,
		false,
		func(ctx context.Context) (bool, error) {
			resp, err := scimClient.Discovery().ServiceProviderConfig()
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

// SetSCIMConfigMapAndWait applies the given SCIM ConfigMap data and polls GET /Users
// until the endpoint returns expectedStatus.
func SetSCIMConfigMapAndWait(client *rancher.Client, scimClient *scimclient.Client, providerName string, data map[string]string, expectedStatus int) error {
	if err := SetSCIMConfigMap(client, providerName, data); err != nil {
		return err
	}
	return WaitForSCIMEndpointStatus(scimClient, expectedStatus)
}

// SetSCIMConfigMapAndWaitCreateReady applies the given SCIM ConfigMap data, waits for GET /Users to
// return 200, and then waits for the write path to accept creates (201). Use this instead of
// SetSCIMConfigMapAndWait in tests that create SCIM resources immediately after a ConfigMap change:
// the read path can become ready before the write path, so a create issued in that window may
// transiently return 500.
func SetSCIMConfigMapAndWaitCreateReady(client *rancher.Client, scimClient *scimclient.Client, providerName string, data map[string]string) error {
	if err := SetSCIMConfigMapAndWait(client, scimClient, providerName, data, http.StatusOK); err != nil {
		return err
	}
	return WaitForSCIMCreateReady(scimClient)
}

// FindFirstThrottledResponse polls GET /Users up to maxAttempts times and returns the first
// response with status 429. Returns nil if no 429 was observed within maxAttempts.
func FindFirstThrottledResponse(scimClient *scimclient.Client, maxAttempts int) (*scimclient.Response, error) {
	for i := 0; i < maxAttempts; i++ {
		resp, err := scimClient.Users().List(nil)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return resp, nil
		}
	}
	return nil, nil
}

// VerifySCIMThrottle reports whether GET /Users returned 429 within maxAttempts.
func VerifySCIMThrottle(scimClient *scimclient.Client, maxAttempts int) (bool, error) {
	resp, err := FindFirstThrottledResponse(scimClient, maxAttempts)
	if err != nil {
		return false, err
	}
	return resp != nil, nil
}

// DecodeJSONBody decodes a SCIM response body as a generic JSON map.
func DecodeJSONBody(resp *scimclient.Response) (map[string]interface{}, error) {
	var body map[string]interface{}
	if err := resp.DecodeJSON(&body); err != nil {
		return nil, err
	}
	return body, nil
}

// PatchUser issues a single-operation PATCH against /Users/{id} and returns the response.
func PatchUser(scimClient *scimclient.Client, userID, op, path string, value interface{}) (*scimclient.Response, error) {
	return scimClient.Users().Patch(userID, scimclient.PatchOp{
		Schemas: []string{scimclient.SCIMSchemaPatchOp},
		Operations: []scimclient.Operation{
			{Op: op, Path: path, Value: value},
		},
	})
}

// PatchGroup issues a single-operation PATCH against /Groups/{id} and returns the response.
func PatchGroup(scimClient *scimclient.Client, groupID, op, path string, value interface{}) (*scimclient.Response, error) {
	return scimClient.Groups().Patch(groupID, scimclient.PatchOp{
		Schemas: []string{scimclient.SCIMSchemaPatchOp},
		Operations: []scimclient.Operation{
			{Op: op, Path: path, Value: value},
		},
	})
}

// WaitForRancherUserName polls Management.User.ByID until the user's Name matches expectedName.
// Transient API errors are treated as "not yet" while the SCIM-created user propagates to the
// management API; the timeout is the authoritative failure signal.
func WaitForRancherUserName(client *rancher.Client, userID, expectedName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		mgmtUser, getErr := client.Management.User.ByID(userID)
		if getErr != nil {
			return false, nil
		}
		return mgmtUser.Name == expectedName, nil
	})
}

// ByIDResult is what one by-ID SCIM request answered
type ByIDResult struct {
	Operation  string
	StatusCode int
	Body       string
}

type scimByIDCall struct {
	operation string
	do        func() (*scimclient.Response, error)
}

func runSCIMByIDCalls(calls []scimByIDCall) ([]ByIDResult, error) {
	results := make([]ByIDResult, 0, len(calls))
	for _, call := range calls {
		resp, err := call.do()
		if err != nil {
			return nil, fmt.Errorf("the by-ID %s request failed: %w", call.operation, err)
		}
		results = append(results, ByIDResult{
			Operation:  call.operation,
			StatusCode: resp.StatusCode,
			Body:       string(resp.Body),
		})
	}
	return results, nil
}

// FetchSCIMUserByIDResults runs every by-ID SCIM user request against the given id, the destructive one last, and returns what each one answered
func FetchSCIMUserByIDResults(scimClient *scimclient.Client, userID, replacementUserName string) ([]ByIDResult, error) {
	replacement := scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: replacementUserName,
	}

	return runSCIMByIDCalls([]scimByIDCall{
		{operation: SCIMOperationGet, do: func() (*scimclient.Response, error) {
			return scimClient.Users().ByID(userID)
		}},
		{operation: SCIMOperationUpdate, do: func() (*scimclient.Response, error) {
			return scimClient.Users().Update(userID, replacement)
		}},
		{operation: SCIMOperationPatch, do: func() (*scimclient.Response, error) {
			return PatchUser(scimClient, userID, scimPatchOpReplace, scimPatchPathActive, false)
		}},
		{operation: SCIMOperationDelete, do: func() (*scimclient.Response, error) {
			return scimClient.Users().Delete(userID)
		}},
	})
}

// FetchSCIMGroupByIDResults runs every by-ID SCIM group request against the given id, the destructive one last, and returns what each one answered
func FetchSCIMGroupByIDResults(scimClient *scimclient.Client, groupID, replacementDisplayName string) ([]ByIDResult, error) {
	replacement := scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		ID:          groupID,
		DisplayName: replacementDisplayName,
	}

	return runSCIMByIDCalls([]scimByIDCall{
		{operation: SCIMOperationGet, do: func() (*scimclient.Response, error) {
			return scimClient.Groups().ByID(groupID)
		}},
		{operation: SCIMOperationUpdate, do: func() (*scimclient.Response, error) {
			return scimClient.Groups().Update(groupID, replacement)
		}},
		{operation: SCIMOperationPatch, do: func() (*scimclient.Response, error) {
			return PatchGroup(scimClient, groupID, scimPatchOpReplace, "externalId", replacementDisplayName)
		}},
		{operation: SCIMOperationDelete, do: func() (*scimclient.Response, error) {
			return scimClient.Groups().Delete(groupID)
		}},
	})
}

// CreateGroupOwnedByProvider creates a Rancher group labelled as owned by the given auth provider and registers its cleanup
func CreateGroupOwnedByProvider(client *rancher.Client, ts *session.Session, providerName string) (*v3.Group, error) {
	group := &v3.Group{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grp-",
			Labels: map[string]string{
				scimAuthProviderLabel: providerName,
			},
		},
		DisplayName: namegen.AppendRandomString("scim-foreign-group"),
		Provider:    providerName,
	}

	logrus.Infof("Creating a Rancher group owned by the %s provider", providerName)
	created, err := client.WranglerContext.Mgmt.Group().Create(group)
	if err != nil {
		return nil, err
	}

	if ts != nil {
		ts.RegisterCleanupFunc(func() error {
			err := client.WranglerContext.Mgmt.Group().Delete(created.Name, &metav1.DeleteOptions{})
			if apierrors.IsNotFound(err) {
				return nil
			}
			return err
		})
	}

	return created, nil
}

// VerifyGroupExists errors when the given Rancher group is absent
func VerifyGroupExists(client *rancher.Client, groupName string) error {
	_, err := client.WranglerContext.Mgmt.Group().Get(groupName, metav1.GetOptions{})
	return err
}

// VerifySCIMProviderScopingEnforced reports whether the Rancher server refuses a by-ID request for an object another provider owns
func VerifySCIMProviderScopingEnforced(client *rancher.Client, scimClient *scimclient.Client, providerName string) (bool, error) {
	probe, err := CreateGroupOwnedByProvider(client, nil, scimProbeProvider)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := client.WranglerContext.Mgmt.Group().Delete(probe.Name, &metav1.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			logrus.WithError(err).Warnf("Failed to delete the provider scoping probe group %s", probe.Name)
		}
	}()

	scoped := true
	pollErr := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true,
		func(ctx context.Context) (bool, error) {
			resp, getErr := scimClient.Groups().ByID(probe.Name)
			if getErr != nil {
				return false, nil
			}

			switch resp.StatusCode {
			case http.StatusNotFound:
				return true, nil
			case http.StatusOK:
				scoped = false
				return true, nil
			default:
				return false, nil
			}
		})
	if pollErr != nil && !kwait.Interrupted(pollErr) {
		return false, pollErr
	}

	return scoped, nil
}

// VerifyByIDResultsStatus errors when any by-ID result carries a status other than the expected one
func VerifyByIDResultsStatus(results []ByIDResult, expected int) error {
	if len(results) == 0 {
		return fmt.Errorf("no by-ID results to check")
	}

	for _, result := range results {
		if result.StatusCode != expected {
			return fmt.Errorf("the by-ID %s request answered %d, want %d: %s",
				result.Operation, result.StatusCode, expected, result.Body)
		}
	}

	return nil
}

// FetchSCIMUserIDs pages through the given provider's SCIM user list and returns every id it carries
func FetchSCIMUserIDs(scimClient *scimclient.Client) ([]string, error) {
	ids := []string{}

	for start := 1; ; {
		query := url.Values{}
		query.Set("startIndex", strconv.Itoa(start))
		query.Set("count", strconv.Itoa(scimListPageSize))

		resp, err := scimClient.Users().List(query)
		if err != nil {
			return nil, err
		}
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("listing SCIM users answered %d: %s", resp.StatusCode, string(resp.Body))
		}

		body, err := DecodeJSONBody(resp)
		if err != nil {
			return nil, err
		}

		resources, _ := body["Resources"].([]interface{})
		for _, resource := range resources {
			entry, ok := resource.(map[string]interface{})
			if !ok {
				continue
			}
			if id, ok := entry["id"].(string); ok {
				ids = append(ids, id)
			}
		}

		total, _ := body["totalResults"].(float64)
		start += len(resources)
		if len(resources) == 0 || start > int(total) {
			return ids, nil
		}
	}
}

// CountSCIMCodes counts 200 and 429 responses in a SCIM result slice. Used by
// rate-limit tests to assert mixed-outcome bursts.
func CountSCIMCodes(results []int) (ok, throttled int) {
	for _, code := range results {
		switch code {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			throttled++
		}
	}
	return ok, throttled
}

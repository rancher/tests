package exttokens

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"net/http"
	"time"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	settingsapi "github.com/rancher/tests/actions/kubeapi/settings"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	UserIDLabel                                       = "cattle.io/user-id"
	ExtTokenStatusCurrentValue                        = false
	ExtTokenStatusExpiredValue                        = false
	ExtTokenAPIPath                                   = "/v1/ext.cattle.io.tokens"
	ExtTokenKindKubeconfig                            = "kubeconfig"
	ExtTokenSecretNamespace                           = "cattle-tokens"
	ExtTokenLastUsedAtField                           = "last-used-at"
	TrueConditionStatus        metav1.ConditionStatus = "True"
	FalseConditionStatus       metav1.ConditionStatus = "False"
)

// NewExtTokenObject returns an unpersisted ext token with a generated name, the given kind, and the given TTL
func NewExtTokenObject(kind string, ttl int64) *extapi.Token {
	return &extapi.Token{
		ObjectMeta: metav1.ObjectMeta{
			Name:   namegen.AppendRandomString("test-exttoken"),
			Labels: map[string]string{},
		},
		Spec: extapi.TokenSpec{
			Kind: kind,
			TTL:  ttl,
		},
	}
}

// CreateExtToken creates an ext token with the TTL value provided using wrangler context and returns the created ext token object
func CreateExtToken(client *rancher.Client, ttlValue int64) (*extapi.Token, error) {
	createdExtToken, err := exttokenapi.CreateExtToken(client, NewExtTokenObject("", ttlValue))
	if err != nil {
		return nil, fmt.Errorf("failed to create ext token: %w", err)
	}

	return createdExtToken, nil
}

// CreateExtSessionToken creates an ext session token using wrangler context and returns the created ext session token object
func CreateExtSessionToken(client *rancher.Client) (*extapi.Token, error) {
	name := namegen.AppendRandomString("test-extsessiontoken")
	extSessionToken := &extapi.Token{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{},
		},
		Spec: extapi.TokenSpec{
			Kind: "session",
		},
	}

	createdExtSessionToken, err := exttokenapi.CreateExtToken(client, extSessionToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create ext session token %w", err)
	}

	return createdExtSessionToken, nil
}

// CreateExtTokenDryRun sends a server-side dry-run create for the ext token and returns the response without persisting it
func CreateExtTokenDryRun(client *rancher.Client, extToken *extapi.Token) (*extapi.Token, error) {
	sharedCtrl, err := client.WranglerContext.ControllerFactory.ForObject(extToken)
	if err != nil {
		return nil, fmt.Errorf("failed to get shared controller for ext token: %w", err)
	}

	dryRunExtToken := &extapi.Token{}
	err = sharedCtrl.Client().Create(context.TODO(), extToken.Namespace, extToken, dryRunExtToken, metav1.CreateOptions{DryRun: []string{metav1.DryRunAll}})
	if err != nil {
		return nil, fmt.Errorf("failed to dry-run create ext token: %w", err)
	}

	return dryRunExtToken, nil
}

// WaitForExtTokenStatusExpired polls until an ext token with the given name has an expired status or the timeout is reached
func WaitForExtTokenStatusExpired(client *rancher.Client, name string, expiredStatus bool) (*extapi.Token, error) {
	var expiredToken *extapi.Token

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		extToken, err := exttokenapi.GetExtTokenByName(client, name)
		if err != nil {
			return false, err
		}
		if extToken.Status.Expired == expiredStatus {
			expiredToken = extToken
			return true, nil
		}
		return false, nil
	})
	return expiredToken, err
}

// WaitForExtTokenToDisable polls until an ext token with the given name is disabled or the timeout is reached
func WaitForExtTokenToDisable(client *rancher.Client, name string, expectedState bool) (*extapi.Token, error) {
	var disabledToken *extapi.Token

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		extToken, err := exttokenapi.GetExtTokenByName(client, name)
		if err != nil {
			return false, err
		}

		if extToken.Spec.Enabled == nil {
			return false, nil
		}

		if *extToken.Spec.Enabled == expectedState {
			disabledToken = extToken
			return true, nil
		}
		return false, nil
	})
	return disabledToken, err
}

// WaitForExtTokenUpdateTTL waits for an ext token's TTL to be updated to the expected value.
func WaitForExtTokenUpdateTTL(client *rancher.Client, token *extapi.Token, expectedTTLMillis int64) (*extapi.Token, error) {
	var updated *extapi.Token

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		var updateErr error
		updated, updateErr = exttokenapi.UpdateExtToken(client, token)
		if updateErr != nil {
			return false, updateErr
		}
		return updated.Spec.TTL == expectedTTLMillis, nil
	})

	if err != nil && updated != nil {
		return updated, fmt.Errorf("timed out waiting for ext token %s TTL to reach %d ms, last observed value was %d ms: %w", token.Name, expectedTTLMillis, updated.Spec.TTL, err)
	}

	return updated, err
}

// AuthenticateWithExtToken creates an R_SESS cookie using the value from the ext token given by name and authenticates against an endpoint
func AuthenticateWithExtToken(baseURL, tokenName, tokenValue, apiPath string) error {
	cookieValue := fmt.Sprintf("ext/%s:%s", tokenName, tokenValue)
	fullURL := baseURL + apiPath

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	cookie := &http.Cookie{
		Name:  "R_SESS",
		Value: cookieValue,
	}
	req.AddCookie(cookie)

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
	}
	client := &http.Client{Transport: transport}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send http request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("authentication failed: expected status 200 OK, but got %d", resp.StatusCode)
	}
	return nil
}

// WaitForUserExtTokensDeletion polls until no ext tokens matching the given label selector exist, or the timeout is reached.
func WaitForUserExtTokensDeletion(client *rancher.Client, labelSelector string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		tokenList, err := exttokenapi.ListExtTokens(client, metav1.ListOptions{LabelSelector: labelSelector})
		if err != nil {
			return false, err
		}
		return len(tokenList.Items) == 0, nil
	})
}

// DeleteLegacyTokenWithExtToken sends a raw HTTP DELETE request to the /v3/tokens endpoint using an ext token for Bearer authentication.
func DeleteLegacyTokenWithExtToken(client *rancher.Client, legacyTokenID string, extTokenValue string) error {
	deleteURL := fmt.Sprintf("https://%s/v3/tokens/%s", client.RancherConfig.Host, legacyTokenID)

	req, err := http.NewRequest(http.MethodDelete, deleteURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create HTTP request: %w", err)
	}

	req.Header.Add("Authorization", fmt.Sprintf("Bearer %s", extTokenValue))

	httpClient := &http.Client{
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("expected status code 200 or 204, got %d", resp.StatusCode)
	}

	return nil
}

// MinutesToMilliseconds converts minutes to the millisecond unit used by general and v3 token TTLs.
func MinutesToMilliseconds(minutes int64) int64 {
	return minutes * settingsapi.MillisecondsPerMinute
}

// MinutesToSeconds converts minutes to the second unit used by kubeconfig token TTLs.
func MinutesToSeconds(minutes int64) int64 {
	return minutes * settingsapi.SecondsPerMinute
}

// CreateExtTokenOfKind creates an ext token of the given kind with the given TTL, in milliseconds.
func CreateExtTokenOfKind(client *rancher.Client, kind string, ttlMillis int64) (*extapi.Token, error) {
	createdToken, err := exttokenapi.CreateExtToken(client, NewExtTokenObject(kind, ttlMillis))
	if err != nil {
		return nil, fmt.Errorf("failed to create ext token of kind %s: %w", kind, err)
	}

	return createdToken, nil
}

// GetExtTokenLastUsedAt reads the lastUsedAt stored in the secret backing the named ext token, or nil when it is unset
func GetExtTokenLastUsedAt(client *rancher.Client, name string) (*time.Time, error) {
	secret, err := client.WranglerContext.Core.Secret().Get(ExtTokenSecretNamespace, name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("reading the secret backing ext token %s: %w", name, err)
	}

	raw := string(secret.Data[ExtTokenLastUsedAtField])
	if raw == "" {
		return nil, nil
	}

	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return nil, fmt.Errorf("parsing the stored %s %q of ext token %s: %w", ExtTokenLastUsedAtField, raw, name, err)
	}

	return &parsed, nil
}

// SetExtTokenLastUsedAt overwrites the lastUsedAt stored in the secret backing the named ext token
func SetExtTokenLastUsedAt(client *rancher.Client, name string, at time.Time) error {
	stamp := base64.StdEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339)))
	patch := []byte(fmt.Sprintf(`{"data":{%q:%q}}`, ExtTokenLastUsedAtField, stamp))

	if _, err := client.WranglerContext.Core.Secret().Patch(ExtTokenSecretNamespace, name, types.MergePatchType, patch); err != nil {
		return fmt.Errorf("setting lastUsedAt in the secret backing ext token %s: %w", name, err)
	}

	return nil
}

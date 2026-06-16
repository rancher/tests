package exttokens

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	UserIDLabel                                       = "cattle.io/user-id"
	ExtTokenStatusCurrentValue                        = false
	ExtTokenStatusExpiredValue                        = false
	TrueConditionStatus        metav1.ConditionStatus = "True"
	FalseConditionStatus       metav1.ConditionStatus = "False"
)

// CreateExtToken creates an ext token with the TTL value provided using wrangler context and returns the created ext token object
func CreateExtToken(client *rancher.Client, ttlValue int64) (*extapi.Token, error) {
	name := namegen.AppendRandomString("test-exttoken")
	extToken := &extapi.Token{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: map[string]string{},
		},
		Spec: extapi.TokenSpec{
			TTL: ttlValue,
		},
	}

	createdExtToken, err := exttokenapi.CreateExtToken(client, extToken)
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

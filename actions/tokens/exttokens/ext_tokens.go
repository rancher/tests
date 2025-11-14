package exttokens

import (
	"context"
	"fmt"
	"crypto/tls"
	"net/http"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/settings"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	UserIDLabel                                         =  "cattle.io/user-id"
	ExtTokenStatusCurrentValue                          = false
	ExtTokenStatusExpiredValue                          = false
	TrueConditionStatus          metav1.ConditionStatus = "True"
	FalseConditionStatus         metav1.ConditionStatus = "False"
)

// CreateExtToken creates an ext token with the TTL value provided using Public API
func CreateExtToken(client *rancher.Client, ttlValue int64) (*extapi.Token, error) {
	name := namegen.AppendRandomString("test-exttoken")
	extToken := &extapi.Token{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
			Labels: map[string]string{},
		},
		Spec: extapi.TokenSpec{
			TTL: ttlValue,
		},
	}

	createdExtToken, err := client.WranglerContext.Ext.Token().Create(extToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create ext token: %w", err)
	}
	return createdExtToken, nil
}

// CreateExtSessionToken creates an ext session token using Public API
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

	createdExtSessionToken, err := client.WranglerContext.Ext.Token().Create(extSessionToken)
	if err != nil {
		return nil, fmt.Errorf("failed to create ext session token %w", err)
	}
	return createdExtSessionToken, nil
}

// GetExtToken retrieves an ext token by name using GET API
func GetExtToken(client *rancher.Client, name string) (*extapi.Token, error) {
	extToken, err := client.WranglerContext.Ext.Token().Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ext token %s: %w", name, err)
	}
	return extToken, nil
}

// ListExtToken retrieves ext tokens using LIST API
func ListExtToken(client *rancher.Client) (*extapi.TokenList, error) {
	extTokens, err := client.WranglerContext.Ext.Token().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ext token: %w", err)
	}
	return extTokens, nil
}

// UpdateExtToken updates an existing ext token using Public API
func UpdateExtToken(client *rancher.Client, exttoken *extapi.Token) (*extapi.Token, error) {
	updatedExtToken, err := client.WranglerContext.Ext.Token().Update(exttoken)
	if err != nil {
		return nil, fmt.Errorf("failed to update ext token: %w", err)
	}
	return updatedExtToken, nil
}

// DeleteExtToken deletes a ext token by name using Public API
func DeleteExtToken(client *rancher.Client, exttokenname string) error {
	err := client.WranglerContext.Ext.Token().Delete(exttokenname, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete ext token: %s: %w", exttokenname, err)
	}

	err = WaitForExtTokenDeletion(client, exttokenname)
	if err != nil {
		return fmt.Errorf("timed out waiting for kubeconfig %s to be deleted: %w", exttokenname, err)
	}
	return nil
}

// GetExtTokenDefaultTTLMinutes fetches the Rancher setting "auth-token-max-ttl-minutes"
func GetExtTokenDefaultTTLMinutes(client *rancher.Client) (string, error) {
	steveClient := client.Steve
	authTokenMaxTTLSetting, err := steveClient.SteveType(settings.ManagementSetting).ByID("auth-token-max-ttl-minutes")
	if err != nil {
		return "", err
	}

	extTokenSetting:= &v3.Setting{}
	err = v1.ConvertToK8sType(authTokenMaxTTLSetting.JSONResp, extTokenSetting)
	if err != nil {
		return "", err
	}

	return extTokenSetting.Value, nil
}

// WaitForExtTokenDeletion polls until an ext token with the given name is deleted or the timeout is reached
func WaitForExtTokenDeletion(client *rancher.Client, name string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := GetExtToken(client, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
			return false, nil
	})
}

// WaitForExtTokenStatusExpired polls until an ext token with the given name has an expired status or the timeout is reached
func WaitForExtTokenStatusExpired(client *rancher.Client, name string, expiredStatus bool) (*extapi.Token, error) {
	var expiredToken *extapi.Token

	err :=  kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		extToken, err := GetExtToken(client, name)
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
        extToken, err := GetExtToken(client, name)
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
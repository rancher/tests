package scim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/clientbase"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
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
				return false, err
			}
			return status == 404, nil
		},
	)
}

// CreateSCIMUser creates a SCIM user with a random username and returns its ID.
func CreateSCIMUser(scimClient *scimclient.Client) (string, error) {
	username := namegen.AppendRandomString("scim-user")
	resp, err := scimClient.Users().Create(scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: username,
	})
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusCreated {
		return "", fmt.Errorf("expected 201, got %d: %s", resp.StatusCode, string(resp.Body))
	}
	return resp.IDFromBody()
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
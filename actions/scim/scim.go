package scim

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/clientbase"
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

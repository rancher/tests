package secrets

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ProjectScopedSecretLabel          = "management.cattle.io/project-scoped-secret"
	ProjectScopedSecretCopyAnnotation = "management.cattle.io/project-scoped-secret-copy"
)

// NewSecretTemplate is a constructor that creates the secret template for secrets
func NewSecretTemplate(secretName, namespace string, data map[string][]byte, secretType corev1.SecretType, labels, annotations map[string]string) corev1.Secret {
	return corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:        secretName,
			Namespace:   namespace,
			Labels:      labels,
			Annotations: annotations,
		},
		Type: secretType,
		Data: data,
	}
}

// NewBasicAuthSecret is a constructor for a Basic Auth secret type
func NewBasicAuthSecret(name, namespace, username, password string) *corev1.Secret {
	return &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
		Data: map[string][]byte{
			"username": []byte(username),
			"password": []byte(password),
		},
		Type: "kubernetes.io/basic-auth",
	}
}

// GetSecretByName is a helper function that uses the wrangler context to get a specific secret in a namespace of a specific cluster.
func GetSecretByName(client *rancher.Client, clusterID, namespace, secretName string, getOpts metav1.GetOptions) (*corev1.Secret, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	secret, err := clusterContext.Core.Secret().Get(namespace, secretName, getOpts)
	if err != nil {
		return nil, err
	}
	return secret, nil
}

// CreateRegistrySecretDockerConfigJSON is a helper to generate dockerconfigjson content for a registry secret
func CreateRegistrySecretDockerConfigJSON(registryconfig *Config) (string, error) {
	registry := registryconfig.Name
	username := registryconfig.Username
	password := registryconfig.Password

	if username == "" || password == "" {
		return "", fmt.Errorf("missing registry credentials in the config file")
	}

	auth := map[string]interface{}{
		"username": username,
		"password": password,
		"auth":     base64.StdEncoding.EncodeToString([]byte(username + ":" + password)),
	}

	config := map[string]interface{}{
		"auths": map[string]interface{}{
			registry: auth,
		},
	}

	configJSON, err := json.Marshal(config)
	if err != nil {
		return "", err
	}

	return string(configJSON), nil
}

// SecretCopyWithNewData is a helper to create a copy of an existing secret with new data.
func SecretCopyWithNewData(secret *corev1.Secret, newData map[string][]byte) *corev1.Secret {
	updatedSecretObj := secret.DeepCopy()
	if updatedSecretObj.Data == nil {
		updatedSecretObj.Data = make(map[string][]byte)
	}

	for key, value := range newData {
		updatedSecretObj.Data[key] = value
	}

	return updatedSecretObj
}

// WaitForSecretInNamespaces waits for a secret to either exist or be deleted in the given list of namespaces.
// If shouldExist is true, it waits for the secret to be present in all namespaces.
// If shouldExist is false, it waits for the secret to be absent from all namespaces.
func WaitForSecretInNamespaces(client *rancher.Client, clusterID, secretName string, namespaceList []*corev1.Namespace, shouldExist bool) error {
	for _, ns := range namespaceList {
		err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
			_, err = GetSecretByName(client, clusterID, ns.Name, secretName, metav1.GetOptions{})
			if shouldExist {
				if err != nil {
					if apierrors.IsNotFound(err) {
						return false, nil
					}
					return false, err
				}
				return true, nil
			} else {
				if err == nil {
					return false, nil
				}
				if apierrors.IsNotFound(err) {
					return true, nil
				}
				return false, err
			}
		})
		if err != nil {
			return fmt.Errorf("waiting for secret %s in namespace %s failed: %w", secretName, ns.Name, err)
		}
	}
	return nil
}

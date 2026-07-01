package secrets

import (
	"fmt"

	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"

	"github.com/rancher/shepherd/clients/rancher"
	corev1 "k8s.io/api/core/v1"
)

// CreateSecret is a helper to create a secret using wrangler context with generated name and provided data
func CreateSecret(client *rancher.Client, clusterID, namespaceName string, data map[string]string, secretType corev1.SecretType, labels, annotations map[string]string) (*corev1.Secret, error) {
	if labels == nil {
		labels = make(map[string]string)
	}
	if annotations == nil {
		annotations = make(map[string]string)
	}

	secretName := namegen.AppendRandomString("testsecret")
	secretTemplate := NewSecretTemplate(secretName, namespaceName, data, secretType, labels, annotations)

	createdSecret, err := extsecretapi.CreateSecretWithTemplate(client, clusterID, &secretTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to create secret: %w", err)
	}

	return createdSecret, nil
}

// CreateProjectScopedSecret creates a project-scoped secret in the project's backing namespace in the local cluster
func CreateProjectScopedSecret(client *rancher.Client, clusterID, projectID string, data map[string]string, secretType corev1.SecretType) (*corev1.Secret, error) {
	backingNamespace := fmt.Sprintf("%s-%s", clusterID, projectID)

	labels := map[string]string{
		ProjectScopedSecretLabel: projectID,
	}

	return CreateSecret(client, extclusterapi.LocalCluster, backingNamespace, data, secretType, labels, nil)
}

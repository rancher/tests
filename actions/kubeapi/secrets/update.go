package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateSecretData updates the data of a secret in the specified namespace using the wrangler context for the given cluster
func UpdateSecretData(client *rancher.Client, clusterID, namespace, secretName string, newData map[string][]byte) (*corev1.Secret, error) {
	var updatedSecret *corev1.Secret
	var lastErr error
	err := kwait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		existingSecret, getErr := extsecretapi.GetSecretByName(client, clusterID, namespace, secretName)
		if getErr != nil {
			lastErr = fmt.Errorf("failed to get secret %s: %w", secretName, getErr)
			return false, nil
		}

		existingSecret.Data = newData

		updatedSecret, lastErr = extsecretapi.UpdateSecret(client, clusterID, existingSecret)
		if lastErr != nil {
			return false, nil
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("timed out updating secret %s/%s: %w", namespace, secretName, lastErr)
	}

	return updatedSecret, nil
}

// UpdateProjectScopedSecretData updates the data of an project-scoped secret in the backing namespace of a project
func UpdateProjectScopedSecretData(client *rancher.Client, clusterID, projectID, secretName string, newData map[string][]byte) (*corev1.Secret, error) {
	backingNamespace := fmt.Sprintf("%s-%s", clusterID, projectID)

	return UpdateSecretData(client, extclusterapi.LocalCluster, backingNamespace, secretName, newData)
}

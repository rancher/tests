package secrets

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateSecretData updates the data of a secret in the specified namespace using the wrangler context for the given cluster
func UpdateSecretData(client *rancher.Client, clusterID, namespace, secretName string, newData map[string][]byte) (*corev1.Secret, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster context: %w", err)
	}

	var updatedSecret *corev1.Secret
	var lastErr error
	err = kwait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		existingSecret, getErr := clusterContext.Core.Secret().Get(namespace, secretName, metav1.GetOptions{})
		if getErr != nil {
			lastErr = fmt.Errorf("failed to get secret %s: %w", secretName, getErr)
			return false, nil
		}
		existingSecret.Data = newData
		updatedSecret, lastErr = clusterContext.Core.Secret().Update(existingSecret)
		if lastErr != nil {
			if errors.IsConflict(lastErr) {
				return false, nil
			}
			return false, lastErr
		}
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("timed out updating secret %s: %w", secretName, lastErr)
	}
	return updatedSecret, nil
}

// UpdateProjectScopedSecretData updates the data of an project-scoped secret in the backing namespace of a project
func UpdateProjectScopedSecretData(client *rancher.Client, clusterID, projectID, secretName string, newData map[string][]byte) (*corev1.Secret, error) {
	backingNamespace := fmt.Sprintf("%s-%s", clusterID, projectID)

	return UpdateSecretData(client, extclusterapi.LocalCluster, backingNamespace, secretName, newData)
}

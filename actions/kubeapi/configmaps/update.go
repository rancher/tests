package configmaps

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	coreV1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateConfigMap replaces the data of a config map in the specified namespace using the wrangler context for the given cluster, retrying on conflict.
func UpdateConfigMap(client *rancher.Client, clusterID, namespace, configMapName string, newData map[string]string) (*coreV1.ConfigMap, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster context: %w", err)
	}

	var updatedConfigMap *coreV1.ConfigMap
	var lastErr error
	err = kwait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, 30*time.Second, false, func(ctx context.Context) (bool, error) {
		existingConfigMap, getErr := clusterContext.Core.ConfigMap().Get(namespace, configMapName, metav1.GetOptions{})
		if getErr != nil {
			lastErr = fmt.Errorf("failed to get config map %s: %w", configMapName, getErr)
			return false, nil
		}
		existingConfigMap.Data = newData
		updatedConfigMap, lastErr = clusterContext.Core.ConfigMap().Update(existingConfigMap)
		if lastErr != nil {
			if apierrors.IsConflict(lastErr) {
				return false, nil
			}
			return false, lastErr
		}
		return true, nil
	})
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return nil, fmt.Errorf("timed out updating config map %s: %w", configMapName, lastErr)
		}
		return nil, fmt.Errorf("failed to update config map %s: %w", configMapName, err)
	}
	return updatedConfigMap, nil
}

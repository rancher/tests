package imported

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	clusterKind               = "Cluster"
	encryptionKeyRotationKind = "EncryptionKeyRotation"
	managementAPIVersion      = "management.cattle.io/v3"
	v1alpha1APIVersion        = "operation.cattle.io/v1alpha1"

	encryptionKeyRotationSteveType = "operation.cattle.io.encryptionkeyrotation"
)

// RotateEncryptionKey rotates the encryption key for an imported cluster.
func RotateEncryptionKey(client *rancher.Client, clusterName string) error {
	rotation := map[string]any{
		"apiVersion": v1alpha1APIVersion,
		"kind":       encryptionKeyRotationKind,
		"metadata": map[string]any{
			"generateName": clusterName + "-",
			"namespace":    clusterName,
		},
		"spec": map[string]any{
			"cancel": false,
			"clusterRef": map[string]any{
				"apiVersion": managementAPIVersion,
				"kind":       clusterKind,
				"name":       clusterName,
			},
			"ttl": 60,
		},
	}

	resourceType := client.Steve.SteveType(encryptionKeyRotationSteveType)
	created, err := resourceType.NamespacedSteveClient(clusterName).Create(rotation)
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		current, err := resourceType.ByID(created.ID)
		if err != nil {
			return false, err
		}

		if current.State == nil {
			return false, fmt.Errorf("encryption key rotation operation %s has no state", created.ID)
		}

		if current.State.Error {
			return false, fmt.Errorf("encryption key rotation operation %s failed: %s", created.ID, current.State.Message)
		}

		return !current.State.Transitioning && current.State.Name != "pending" && current.State.Name != "restart", nil
	})
}

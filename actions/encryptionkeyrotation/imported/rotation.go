package imported

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/clusters/imported"
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

	if err := imported.WaitForOperation(client, encryptionKeyRotationSteveType, created.ID); err != nil {
		return err
	}

	return nil
}

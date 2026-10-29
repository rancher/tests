package imported

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/clusters/imported"
)

const (
	certRotationKind     = "CertificateRotation"
	managementAPIVersion = "management.cattle.io/v3"
	v1alpha1APIVersion   = "operation.cattle.io/v1alpha1"

	certRotationSteveType = "operation.cattle.io.certificaterotation"
)

// RotateCertificates is a helper function that performs a certificate rotation for an imported cluster.
func RotateCertificates(client *rancher.Client, clusterName string) error {
	certificates := map[string]any{
		"apiVersion": v1alpha1APIVersion,
		"kind":       certRotationKind,
		"metadata": map[string]any{
			"generateName": clusterName + "-",
			"namespace":    clusterName,
		},
		"spec": map[string]any{
			"cancel": false,
			"clusterRef": map[string]any{
				"apiVersion": managementAPIVersion,
				"kind":       "Cluster",
				"name":       clusterName,
			},
			"ttl": 60,
		},
	}

	resourceType := client.Steve.SteveType(certRotationSteveType)
	created, err := resourceType.NamespacedSteveClient(clusterName).Create(certificates)
	if err != nil {
		return err
	}

	if err := imported.WaitForOperation(client, certRotationSteveType, created.ID); err != nil {
		return err
	}

	return nil
}

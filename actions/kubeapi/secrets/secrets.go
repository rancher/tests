package secrets

import (
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/rancher/shepherd/clients/rancher"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretGroupVersionResource is the required Group Version Resource for accessing secrets in a cluster,
// using the dynamic client.
var SecretGroupVersionResource = schema.GroupVersionResource{
	Group:    "",
	Version:  "v1",
	Resource: "secrets",
}

// GetSecretByName is a helper function that uses the wrangler context to get a specific secret in a namespace of a specific cluster.
func GetSecretByName(client *rancher.Client, clusterID, namespace, secretName string, getOpts metav1.GetOptions) (*coreV1.Secret, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	secret, err := wranglerContext.Core.Secret().Get(namespace, secretName, getOpts)
	if err != nil {
		return nil, err
	}
	return secret, nil
}

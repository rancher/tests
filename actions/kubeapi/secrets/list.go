package secrets

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretList is a struct that contains a list of secrets.
type SecretList struct {
	Items []corev1.Secret
}

// ListSecrets is a helper function that uses the wrangler context to list secrets in a cluster
func ListSecrets(client *rancher.Client, clusterID, namespace string, listOpts metav1.ListOptions) (*SecretList, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster context: %w", err)
	}

	secrets, err := clusterContext.Core.Secret().List(namespace, listOpts)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	return &SecretList{Items: secrets.Items}, nil
}

// Names is a method that accepts SecretList as a receiver,
// returns each secret name in the list as a new slice of strings.
func (list *SecretList) Names() []string {
	var secretNames []string

	for _, secret := range list.Items {
		secretNames = append(secretNames, secret.Name)
	}

	return secretNames
}

package resourcequotas

import (
	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetResourceQuotaByName is a helper function that uses the wrangler context to get a ResourceQuota by name from a given namespace of a cluster.
func GetResourceQuotaByName(client *rancher.Client, clusterID, namespaceName, resourceQuotaName string) (*corev1.ResourceQuota, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	resourceQuota, err := clusterContext.Core.ResourceQuota().Get(namespaceName, resourceQuotaName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return resourceQuota, nil
}

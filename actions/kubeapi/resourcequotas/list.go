package resourcequotas

import (
	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ResourceQuotaList is a struct that contains a list of resource quotas.
type ResourceQuotaList struct {
	Items []corev1.ResourceQuota
}

// ListResourceQuotas is a helper function that uses wrangler context to list all ResourceQuotas in a given namespace of a cluster.
func ListResourceQuotas(client *rancher.Client, clusterID, namespaceName string, listOpts metav1.ListOptions) (*ResourceQuotaList, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	resourceQuotas, err := clusterContext.Core.ResourceQuota().List(namespaceName, listOpts)
	if err != nil {
		return nil, err
	}

	return &ResourceQuotaList{Items: resourceQuotas.Items}, nil
}

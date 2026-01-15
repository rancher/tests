package namespaces

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/api/scheme"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetNamespacesInProject retrieves all namespaces in a specific project within a cluster
func GetNamespacesInProject(client *rancher.Client, clusterID, projectName string) ([]*corev1.Namespace, error) {
	clusterContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get wrangler context for cluster %s: %w", clusterID, err)
	}

	nsList, err := clusterContext.Core.Namespace().List(metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", ProjectIDAnnotation, projectName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces for project %s: %w", projectName, err)
	}

	namespaces := make([]*corev1.Namespace, 0, len(nsList.Items))
	for i := range nsList.Items {
		namespaces = append(namespaces, &nsList.Items[i])
	}

	return namespaces, nil
}

// NamespaceList is a struct that contains a list of namespaces.
type NamespaceList struct {
	Items []corev1.Namespace
}

// ListNamespaces is a helper function that uses the dynamic client to list namespaces in a cluster with its list options.
func ListNamespaces(client *rancher.Client, clusterID string, listOpts metav1.ListOptions) (*NamespaceList, error) {
	namespaceList := new(NamespaceList)

	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	namespaceResource := dynamicClient.Resource(NamespaceGroupVersionResource).Namespace("")
	namespaces, err := namespaceResource.List(context.TODO(), listOpts)
	if err != nil {
		return nil, err
	}

	for _, unstructuredNamespace := range namespaces.Items {
		newNamespace := &corev1.Namespace{}
		err := scheme.Scheme.Convert(&unstructuredNamespace, newNamespace, unstructuredNamespace.GroupVersionKind())
		if err != nil {
			return nil, err
		}

		namespaceList.Items = append(namespaceList.Items, *newNamespace)
	}

	return namespaceList, nil
}

// Names is a method that accepts NamespaceList as a receiver,
// returns each namespace name in the list as a new slice of strings.
func (list *NamespaceList) Names() []string {
	var namespaceNames []string

	for _, namespace := range list.Items {
		namespaceNames = append(namespaceNames, namespace.Name)
	}

	return namespaceNames
}

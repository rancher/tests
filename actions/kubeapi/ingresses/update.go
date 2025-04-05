package ingresses

import (
	"context"

	networkingv1 "k8s.io/api/networking/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
)

// UpdateIngress updates an existing ingress with new specifications
func UpdateIngress(client *rancher.Client, clusterID, namespace string, existingIngress *networkingv1.Ingress, updatedIngress *networkingv1.Ingress) (*networkingv1.Ingress, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	ingressResource := dynamicClient.Resource(IngressesGroupVersionResource).Namespace(namespace)

	ingressUnstructured, err := ingressResource.Get(context.TODO(), existingIngress.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	currentIngress := &networkingv1.Ingress{}
	err = scheme.Scheme.Convert(ingressUnstructured, currentIngress, ingressUnstructured.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	updatedIngress.ObjectMeta.ResourceVersion = currentIngress.ObjectMeta.ResourceVersion

	unstructuredResp, err := ingressResource.Update(context.TODO(), unstructured.MustToUnstructured(updatedIngress), metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	newIngress := &networkingv1.Ingress{}
	err = scheme.Scheme.Convert(unstructuredResp, newIngress, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	return newIngress, nil
}

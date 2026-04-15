package horizontalpodautoscalers

import (
	"context"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpdateHPA is a helper function that uses the dynamic client to update a horizontal pod autoscaler in a namespace for a specific cluster.
func UpdateHPA(client *rancher.Client, clusterID, namespace string, existingHPA *autoscalingv2.HorizontalPodAutoscaler, updatedHPA *autoscalingv2.HorizontalPodAutoscaler) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)

	hpaUnstructured, err := hpaResource.Get(context.TODO(), existingHPA.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	currentHPA := &autoscalingv2.HorizontalPodAutoscaler{}
	err = scheme.Scheme.Convert(hpaUnstructured, currentHPA, hpaUnstructured.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	updatedHPA.ObjectMeta.ResourceVersion = currentHPA.ObjectMeta.ResourceVersion

	unstructuredResp, err := hpaResource.Update(context.TODO(), unstructured.MustToUnstructured(updatedHPA), metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	newHPA := &autoscalingv2.HorizontalPodAutoscaler{}
	err = scheme.Scheme.Convert(unstructuredResp, newHPA, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	return newHPA, nil
}

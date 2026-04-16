package autoscaling

import (
	"context"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpdateHPA is a helper function that uses the dynamic client to update a HorizontalPodAutoscaler in a cluster.
func UpdateHPA(client *rancher.Client, clusterID, namespace string, hpa *autoscalingv2.HorizontalPodAutoscaler) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)

	// Get the latest version to ensure we have the correct resource version
	latestUnstructured, err := hpaResource.Get(context.TODO(), hpa.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	hpa.ResourceVersion = latestUnstructured.GetResourceVersion()

	unstructuredResp, err := hpaResource.Update(context.TODO(), unstructured.MustToUnstructured(hpa), metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	updatedHPA := &autoscalingv2.HorizontalPodAutoscaler{}
	err = scheme.Scheme.Convert(unstructuredResp, updatedHPA, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	return updatedHPA, nil
}

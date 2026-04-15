package horizontalpodautoscalers

import (
	"context"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateHPA is a helper function that uses the dynamic client to create a horizontal pod autoscaler on a namespace for a specific cluster.
func CreateHPA(client *rancher.Client, clusterID, namespace string, hpa *autoscalingv2.HorizontalPodAutoscaler) (*autoscalingv2.HorizontalPodAutoscaler, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)

	unstructuredResp, err := hpaResource.Create(context.TODO(), unstructured.MustToUnstructured(hpa), metav1.CreateOptions{})
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

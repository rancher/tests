package horizontalpodautoscalers

import (
	"context"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/api/scheme"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// HPAList is a struct that contains a list of horizontal pod autoscalers.
type HPAList struct {
	Items []autoscalingv2.HorizontalPodAutoscaler
}

// ListHPAs is a helper function that uses the dynamic client to list horizontal pod autoscalers on a namespace for a specific cluster with its list options.
func ListHPAs(client *rancher.Client, clusterID, namespace string, listOpts metav1.ListOptions) (*HPAList, error) {
	hpaList := new(HPAList)

	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}
	hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)
	hpas, err := hpaResource.List(context.TODO(), listOpts)
	if err != nil {
		return nil, err
	}

	for _, unstructuredHPA := range hpas.Items {
		newHPA := &autoscalingv2.HorizontalPodAutoscaler{}
		err := scheme.Scheme.Convert(&unstructuredHPA, newHPA, unstructuredHPA.GroupVersionKind())
		if err != nil {
			return nil, err
		}

		hpaList.Items = append(hpaList.Items, *newHPA)
	}

	return hpaList, nil
}

// Names is a method that accepts HPAList as a receiver,
// returns each HPA name in the list as a new slice of strings.
func (list *HPAList) Names() []string {
	var hpaNames []string

	for _, hpa := range list.Items {
		hpaNames = append(hpaNames, hpa.Name)
	}

	return hpaNames
}

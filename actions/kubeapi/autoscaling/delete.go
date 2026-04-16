package autoscaling

import (
	"context"

	"github.com/rancher/shepherd/clients/rancher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// DeleteHPA is a helper function that uses the dynamic client to delete a HorizontalPodAutoscaler on a namespace for a specific cluster.
func DeleteHPA(client *rancher.Client, clusterID, namespace, name string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)

	return hpaResource.Delete(context.TODO(), name, metav1.DeleteOptions{})
}

package horizontalpodautoscalers

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// DeleteHPA is a helper function that uses the dynamic client to delete a horizontal pod autoscaler from a cluster.
func DeleteHPA(client *rancher.Client, clusterID, namespace, hpaName string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	hpaResource := dynamicClient.Resource(HPAGroupVersionResource).Namespace(namespace)

	err = hpaResource.Delete(context.TODO(), hpaName, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.TenSecondTimeout, false, func(ctx context.Context) (done bool, err error) {
		hpaList, err := ListHPAs(client, clusterID, namespace, metav1.ListOptions{
			FieldSelector: "metadata.name=" + hpaName,
		})
		if err != nil {
			return false, err
		}

		if len(hpaList.Items) == 0 {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return fmt.Errorf("HPA %s was not deleted within timeout: %w", hpaName, err)
	}

	return nil
}

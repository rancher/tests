package ingresses

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
)

// DeleteIngress is a helper function that uses the dynamic client to delete a ingress from a cluster
func DeleteIngress(client *rancher.Client, clusterID, namespace, ingressName string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	ingressResource := dynamicClient.Resource(IngressesGroupVersionResource).Namespace(namespace)

	err = ingressResource.Delete(context.TODO(), ingressName, metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	err = wait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.TenSecondTimeout, false, func(ctx context.Context) (done bool, err error) {
		ingressList, err := ListIngresses(client, clusterID, namespace, metav1.ListOptions{
			FieldSelector: "metadata.name=" + ingressName,
		})

		if err != nil {
			return false, err
		}

		if len(ingressList.Items) == 0 {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return err
	}

	return nil
}

package statefulset

import (
	"context"
	"time"

	"github.com/rancher/rancher/pkg/api/scheme"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/kubeapi/workloads/statefulsets"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func VerifyStatefulset(client *rancher.Client, clusterID, namespace, statefulsetName string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	statefulSetResource := dynamicClient.Resource(statefulsets.StatefulSetGroupVersionResource).Namespace(namespace)

	err = kwait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaults.OneMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		unstructuredResp, err := statefulSetResource.Get(context.TODO(), statefulsetName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		statefulset := &appv1.StatefulSet{}
		err = scheme.Scheme.Convert(unstructuredResp, statefulset, unstructuredResp.GroupVersionKind())
		if err != nil {
			return false, err
		}

		if *statefulset.Spec.Replicas == statefulset.Status.AvailableReplicas {
			return true, nil
		}

		return false, nil
	})

	return err
}

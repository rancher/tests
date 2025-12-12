package daemonset

import (
	"context"
	"time"

	"github.com/rancher/rancher/pkg/api/scheme"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/kubeapi/workloads/daemonsets"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyDaemonset verifies that a daemonset is active
func VerifyDaemonset(client *rancher.Client, clusterID, namespace, daemonsetName string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	daemonsetResource := dynamicClient.Resource(daemonsets.DaemonSetGroupVersionResource).Namespace(namespace)

	err = kwait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaults.OneMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		unstructuredResp, err := daemonsetResource.Get(context.TODO(), daemonsetName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		daemonset := &appv1.DaemonSet{}
		err = scheme.Scheme.Convert(unstructuredResp, daemonset, unstructuredResp.GroupVersionKind())
		if err != nil {
			return false, err
		}

		if daemonset.Status.DesiredNumberScheduled == daemonset.Status.NumberReady {
			return true, nil
		}

		return false, nil
	})

	return err
}

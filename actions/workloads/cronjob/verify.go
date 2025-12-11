package cronjob

import (
	"context"
	"time"

	"github.com/rancher/rancher/pkg/api/scheme"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/kubeapi/workloads/cronjobs"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func VerifyCronJob(client *rancher.Client, clusterID, namespace, cronJobName string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	cronjobResource := dynamicClient.Resource(cronjobs.CronJobGroupVersionResource).Namespace(namespace)

	err = kwait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		unstructuredResp, err := cronjobResource.Get(context.TODO(), cronJobName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		cronJob := &batchv1.CronJob{}
		err = scheme.Scheme.Convert(unstructuredResp, cronJob, unstructuredResp.GroupVersionKind())
		if err != nil {
			return false, err
		}

		if len(cronJob.Status.Active) > 0 {
			return true, nil
		}

		return false, nil
	})

	return err
}

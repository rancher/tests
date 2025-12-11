package job

import (
	"context"
	"time"

	"github.com/rancher/rancher/pkg/api/scheme"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/kubeapi/workloads/jobs"
	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyJob verifies that a job is active
func VerifyJob(client *rancher.Client, clusterID, namespace, jobName string) error {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	jobResource := dynamicClient.Resource(jobs.JobGroupVersionResource).Namespace(namespace)

	err = kwait.PollUntilContextTimeout(context.TODO(), 1*time.Second, defaults.OneMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		unstructuredResp, err := jobResource.Get(context.TODO(), jobName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		job := &batchv1.Job{}
		err = scheme.Scheme.Convert(unstructuredResp, job, unstructuredResp.GroupVersionKind())
		if err != nil {
			return false, err
		}

		if job.Status.Succeeded == 1 {
			return true, nil
		}

		return false, nil
	})

	return err
}

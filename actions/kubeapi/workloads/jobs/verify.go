package job

import (
	"context"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extjobapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/jobs"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyJobStatus verifies that the Job with the given name in the specified namespace has completed successfully
func VerifyJobStatus(client *rancher.Client, clusterID, namespace, jobName string) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		job, err := extjobapi.GetJobByName(client, clusterID, namespace, jobName)
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

package cronjob

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/wait"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
)

// CreateCronJob is a helper to create a cronjob in a namespace using wrangler context
func CreateCronJob(client *rancher.Client, clusterID, namespaceName, schedule string, podTemplate corev1.PodTemplateSpec, watchCronJob bool) (*batchv1.CronJob, error) {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	cronJobTemplate := NewCronJobTemplate(namespaceName, schedule, podTemplate)
	createdCronJob, err := ctx.Batch.CronJob().Create(cronJobTemplate)

	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	if watchCronJob {
		err = WatchAndWaitCronJobWrangler(client, clusterID, namespaceName, createdCronJob)
		if err != nil {
			return nil, err
		}
	}

	return createdCronJob, nil
}

// WatchAndWaitCronJobWrangler is a helper to watch and wait for cronjob to be active using wrangler context
func WatchAndWaitCronJobWrangler(client *rancher.Client, clusterID, namespaceName string, cronJob *batchv1.CronJob) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	listOptions := metav1.ListOptions{
		FieldSelector:  "metadata.name=" + cronJob.Name,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	}

	watchInterface, err := ctx.Batch.CronJob().Watch(namespaceName, listOptions)
	if err != nil {
		return err
	}

	return wait.WatchWait(watchInterface, func(event watch.Event) (bool, error) {
		cronJob, ok := event.Object.(*batchv1.CronJob)
		if !ok {
			return false, fmt.Errorf("failed to cast to batchv1.CronJob")
		}

		if len(cronJob.Status.Active) > 0 {
			return true, nil
		}
		return false, nil
	})
}

// DeleteCronJob is a helper to delete a cronjob in a namespace using wrangler context
func DeleteCronJob(client *rancher.Client, clusterID string, cronjob *batchv1.CronJob, waitForDelete bool) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = wranglerContext.Batch.CronJob().Delete(cronjob.Namespace, cronjob.Name, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete cronjob: %w", err)
	}

	if waitForDelete {
		err = WaitForDeleteCronJob(client, clusterID, cronjob)
		if err != nil {
			return err
		}
	}

	return nil
}

// WaitForDeleteCronJob is a helper to wait for cronjob to delete
func WaitForDeleteCronJob(client *rancher.Client, clusterID string, cronjob *batchv1.CronJob) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		updatedCronJobList, pollErr := wranglerContext.Batch.CronJob().List(cronjob.Namespace, metav1.ListOptions{})
		if pollErr != nil {
			return false, fmt.Errorf("failed to list cronjobs: %w", pollErr)
		}

		if len(updatedCronJobList.Items) == 0 {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("failed to wait for cronjob to delete: %w", err)
	}

	return nil
}

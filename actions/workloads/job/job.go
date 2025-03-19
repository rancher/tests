package job

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

const (
	nginxImageName = "public.ecr.aws/docker/library/nginx"
)

// CreateJob is a helper to create a job in a namespace using wrangler context
func CreateJob(client *rancher.Client, clusterID, namespaceName string, podTemplate corev1.PodTemplateSpec, watchJob bool) (*batchv1.Job, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	jobTemplate := NewJobTemplate(namespaceName, podTemplate)
	createdJob, err := wranglerContext.Batch.Job().Create(&jobTemplate)

	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	if watchJob {
		err = WatchAndWaitJobWrangler(client, clusterID, namespaceName, createdJob)
		if err != nil {
			return nil, err
		}
	}

	return createdJob, nil
}

// WatchAndWaitJobWrangler is a helper to watch and wait for job to be active using wrangler context
func WatchAndWaitJobWrangler(client *rancher.Client, clusterID, namespaceName string, job *batchv1.Job) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	listOptions := metav1.ListOptions{
		FieldSelector:  "metadata.name=" + job.Name,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	}

	watchInterface, err := wranglerContext.Batch.Job().Watch(namespaceName, listOptions)
	if err != nil {
		return err
	}

	return wait.WatchWait(watchInterface, func(event watch.Event) (bool, error) {
		eventJob, ok := event.Object.(*batchv1.Job)
		if !ok {
			return false, fmt.Errorf("failed to cast to batchv1.Job")
		}

		if eventJob.Status.Active > 0 {
			return true, nil
		}
		return false, nil
	})
}

// DeleteJob is a helper to delete a job in a namespace using wrangler context
func DeleteJob(client *rancher.Client, clusterID string, job *batchv1.Job, waitForDelete bool) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = wranglerContext.Batch.Job().Delete(job.Namespace, job.Name, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete job: %w", err)
	}

	if waitForDelete {
		err = WaitForDeleteJob(client, clusterID, job)
		if err != nil {
			return err
		}
	}

	return nil
}

// WaitForDeleteJob is a helper to wait for job to delete
func WaitForDeleteJob(client *rancher.Client, clusterID string, job *batchv1.Job) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		updatedJobList, pollErr := wranglerContext.Batch.Job().List(job.Namespace, metav1.ListOptions{})
		if pollErr != nil {
			return false, fmt.Errorf("failed to list jobs: %w", pollErr)
		}

		if len(updatedJobList.Items) == 0 {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("failed to wait for job to delete: %w", err)
	}

	return nil
}

package job

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	extjobapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/jobs"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// CreateJob is a helper to create a job in a namespace using wrangler context
func CreateJob(client *rancher.Client, clusterID, namespaceName string, podTemplate corev1.PodTemplateSpec, watchJob bool) (*batchv1.Job, error) {
	jobTemplate := NewJobTemplate(namespaceName, podTemplate)
	createdJob, err := extjobapi.CreateJobWithTemplate(client, clusterID, &jobTemplate, watchJob)
	if err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	return createdJob, nil
}

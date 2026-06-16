package job

import (
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	batchv1 "k8s.io/api/batch/v1"
)

// CreateJobFromConfig creates a job from a config using steve
func CreateJobFromConfig(client *v1.Client, clusterID string, job *batchv1.Job) (*batchv1.Job, error) {
	jobResp, err := client.SteveType("batch.job").Create(job)
	if err != nil {
		return nil, err
	}

	newJob := new(batchv1.Job)
	err = v1.ConvertToK8sType(jobResp.JSONResp, newJob)
	if err != nil {
		return nil, err
	}

	return newJob, nil
}

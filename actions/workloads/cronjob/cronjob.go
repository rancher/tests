package cronjob

import (
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	batchv1 "k8s.io/api/batch/v1"
)

// CreateCronJobFromConfig creates a cronjob from a config using steve
func CreateCronJobFromConfig(client *v1.Client, clusterID string, cronjob *batchv1.CronJob) (*batchv1.CronJob, error) {
	cronjobResp, err := client.SteveType("batch.cronjob").Create(cronjob)
	if err != nil {
		return nil, err
	}

	newCronJob := new(batchv1.CronJob)
	err = v1.ConvertToK8sType(cronjobResp.JSONResp, newCronJob)
	if err != nil {
		return nil, err
	}

	return newCronJob, nil
}

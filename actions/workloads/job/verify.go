package job

import (
	"github.com/rancher/shepherd/clients/rancher"
	projects "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/workloads/pods"
)

func VerifyCreateJob(client *rancher.Client, clusterID string) error {
	_, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(client, clusterID)
	if err != nil {
		return err
	}

	podTemplate := pods.CreateContainerAndPodTemplate()

	_, err = CreateJob(client, clusterID, namespace.Name, podTemplate, true)
	if err != nil {
		return err
	}

	return nil
}

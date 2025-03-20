package cronjob

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
)

func VerifyCreateCronjob(client *rancher.Client, clusterID string) error {
	_, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(client, clusterID)
	if err != nil {
		return err
	}

	podTemplate := pods.CreateContainerAndPodTemplate()

	logrus.Info("Creating new cronjob and waiting for it to come up active")
	_, err = CreateCronJob(client, clusterID, namespace.Name, "*/1 * * * *", podTemplate, true)
	if err != nil {
		return err
	}

	return nil
}

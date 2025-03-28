package statefulset

import (
	"github.com/rancher/shepherd/clients/rancher"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
)

func VerifyCreateStatefulset(client *rancher.Client, clusterID string) error {
	_, namespace, err := projectsapi.CreateProjectAndNamespaceUsingWrangler(client, clusterID)
	if err != nil {
		return err
	}

	podTemplate := pods.CreateContainerAndPodTemplate()

	logrus.Infof("Creating new statefulset")
	_, err = CreateStatefulSet(client, clusterID, namespace.Name, podTemplate, 1, true)
	if err != nil {
		return err
	}

	return nil
}

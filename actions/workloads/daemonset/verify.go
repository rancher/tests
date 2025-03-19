package daemonset

import (
	"github.com/rancher/shepherd/clients/rancher"
	projectsapi "github.com/rancher/tests/actions/projects"
	"github.com/sirupsen/logrus"
)

func VerifyCreateDaemonSet(client *rancher.Client, clusterID string) error {
	_, namespace, err := projectsapi.CreateProjectAndNamespaceUsingWrangler(client, clusterID)
	if err != nil {
		return err
	}

	logrus.Info("Creating new daemonset")
	_, err = CreateDaemonset(client, clusterID, namespace.Name, 1, "", "", false, false, true)
	if err != nil {
		return err
	}

	return nil
}

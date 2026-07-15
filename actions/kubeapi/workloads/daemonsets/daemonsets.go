package daemonsets

import (
	"github.com/rancher/shepherd/clients/rancher"
	extdaemonsetsapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	extworkloads "github.com/rancher/shepherd/extensions/workloads"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	appv1 "k8s.io/api/apps/v1"
)

// CreateDaemonset is a helper to create a daemonset using wrangler context with the specified parameters. If waitForReady is true, it will wait for the DaemonSet to be ready after creation.
func CreateDaemonset(client *rancher.Client, clusterID, namespaceName string, imageName string, replicaCount int, secretName, configMapName string, useEnvVars, useVolumes, waitForReady bool) (*appv1.DaemonSet, error) {
	if imageName == "" {
		imageName = deploymentapi.NginxImageName
	}

	deploymentTemplate, err := deploymentapi.CreateDeployment(client, clusterID, namespaceName, imageName, replicaCount, secretName, configMapName, useEnvVars, useVolumes, false, true)
	if err != nil {
		return nil, err
	}

	daemonsetTemplate := extworkloads.NewDaemonSetTemplate(deploymentTemplate.Name, namespaceName, deploymentTemplate.Spec.Template, true, nil)
	createdDaemonset, err := extdaemonsetsapi.CreateDaemonSetWithTemplate(client, clusterID, daemonsetTemplate, waitForReady)
	if err != nil {
		return nil, err
	}

	return createdDaemonset, nil
}

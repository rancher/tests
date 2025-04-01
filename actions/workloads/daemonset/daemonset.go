package daemonset

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/workloads"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/rancher/tests/actions/workloads/deployment"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateDaemonset is a helper to create a daemonset using wrangler context
func CreateDaemonset(client *rancher.Client, clusterID, namespaceName string, replicaCount int, secretName, configMapName string, useEnvVars, useVolumes, watchDaemonset bool) (*appv1.DaemonSet, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	deploymentTemplate, err := deployment.CreateDeployment(client, clusterID, namespaceName, replicaCount, secretName, configMapName, useEnvVars, useVolumes, false, true)
	if err != nil {
		return nil, err
	}

	daemonsetTemplate := workloads.NewDaemonSetTemplate(deploymentTemplate.Name, namespaceName, deploymentTemplate.Spec.Template, true, nil)
	createdDaemonset, err := wranglerContext.Apps.DaemonSet().Create(daemonsetTemplate)
	if err != nil {
		return nil, err
	}

	if watchDaemonset {
		err = charts.WatchAndWaitDaemonSets(client, clusterID, namespaceName, metav1.ListOptions{
			FieldSelector: "metadata.name=" + createdDaemonset.Name,
		})
		if err != nil {
			return nil, err
		}
	}

	return createdDaemonset, nil
}

// UpdateDaemonset is a helper to update daemonsets using wrangler context
func UpdateDaemonset(client *rancher.Client, clusterID, namespaceName string, daemonset *appv1.DaemonSet, watchDaemonset bool) (*appv1.DaemonSet, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	latestDaemonset, err := wranglerContext.Apps.DaemonSet().Get(namespaceName, daemonset.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	daemonset.ResourceVersion = latestDaemonset.ResourceVersion

	updatedDaemonset, err := wranglerContext.Apps.DaemonSet().Update(daemonset)
	if err != nil {
		return nil, err
	}

	if watchDaemonset {
		err = charts.WatchAndWaitDaemonSets(client, clusterID, namespaceName, metav1.ListOptions{
			FieldSelector: "metadata.name=" + updatedDaemonset.Name,
		})
		if err != nil {
			return nil, err
		}
	}

	return updatedDaemonset, nil
}

// DeleteDaemonset is a helper to delete a daemonset using wrangler context
func DeleteDaemonset(client *rancher.Client, clusterID string, daemonset *appv1.DaemonSet) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = wranglerContext.Apps.DaemonSet().Delete(daemonset.Namespace, daemonset.Name, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

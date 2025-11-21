package charts

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	shepherd_charts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	longhornChartName = "longhorn"
)

type LonghornGlobalSettingPut struct {
	Links map[string]string `json:"links"`
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Type  string            `json:"type"`
	Value string            `json:"value"`
}

// InstallLonghornChart installs ther Longhorn chart on the cluster related to the received catalog client object.
// Enable the wait parameter if you wish for this function to wait for all deployments and daemonsets to be ready before returning.
func InstallLonghornChart(client *rancher.Client, payload PayloadOpts, values map[string]interface{}, wait bool) error {
	catalogClient, err := client.GetClusterCatalogClient(payload.Cluster.ID)
	if err != nil {
		return err
	}

	chartInstalls := []types.ChartInstall{
		*NewChartInstall(longhornChartName+"-crd", payload.Version, payload.Cluster.ID, payload.Cluster.Name, payload.Host, catalog.RancherChartRepo, payload.ProjectID, payload.DefaultRegistry, nil),
		*NewChartInstall(longhornChartName, payload.Version, payload.Cluster.ID, payload.Cluster.Name, payload.Host, catalog.RancherChartRepo, payload.ProjectID, payload.DefaultRegistry, values),
	}

	chartInstallAction := NewChartInstallAction(payload.Namespace, payload.ProjectID, chartInstalls)

	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return UninstallLonghornChart(client, payload.Namespace, payload.Cluster.ID, payload.Host)
	})

	if !wait {
		return nil
	}

	err = shepherd_charts.WaitChartInstall(catalogClient, payload.Namespace, longhornChartName)
	if err != nil {
		return err
	}

	err = shepherd_charts.WatchAndWaitDeployments(client, payload.Cluster.ID, payload.Namespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	err = shepherd_charts.WatchAndWaitDaemonSets(client, payload.Cluster.ID, payload.Namespace, metav1.ListOptions{})
	return err
}

// UninstallLonghornChart removes Longhorn from the cluster related to the received catalog client object.
func UninstallLonghornChart(client *rancher.Client, namespace string, clusterID string, rancherHostname string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	// TO uninstall Longhorn successfully it is first needed to set the "deleting-confirmation-flag" on Longhorn settings.
	setDeletionFlagCommand := []string{"kubectl", "-n", namespace, "patch", "settings.longhorn.io", "deleting-confirmation-flag", "--type=merge", "-p", `'{"value": "true"}'`}
	_, err = kubectl.Command(client, nil, clusterID, setDeletionFlagCommand, "")
	if err != nil {
		return err
	}

	err = catalogClient.UninstallChart(longhornChartName, namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	err = catalogClient.UninstallChart(longhornChartName+"-crd", namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	err = waitUninstallation(catalogClient, namespace, longhornChartName+"-crd")
	if err != nil {
		return err
	}

	return waitUninstallation(catalogClient, namespace, longhornChartName)
}

func waitUninstallation(catalogClient *catalog.Client, namespace string, chartName string) error {
	watchAppInterface, err := catalogClient.Apps(namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + chartName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	return wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		switch event.Type {
		case watch.Error:
			return false, fmt.Errorf("there was an error uninstalling Longhorn chart")
		case watch.Deleted:
			return true, nil
		}
		return false, nil
	})
}

package charts

import (
	"context"
	"errors"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

var (
	LonghornNamespace     = "longhorn-system"
	LonghornChartName     = "longhorn"
	enableDeletionSetting = map[string]any{
		"defaultSettings": map[string]any{
			"deletingConfirmationFlag": true,
		},
	}
)

type LonghornGlobalSettingPut struct {
	Links map[string]string `json:"links"`
	ID    string            `json:"id"`
	Name  string            `json:"name"`
	Type  string            `json:"type"`
	Value string            `json:"value"`
}

// InstallLonghornChart installs the Longhorn chart on the cluster according to data on the payload.
// Extra values can be passed in through the values argument.
// This also waits for installation to complete and checks if the deployments are Ready.
func InstallLonghornChart(client *rancher.Client, payload PayloadOpts, values map[string]interface{}) error {
	catalogClient, err := client.GetClusterCatalogClient(payload.Cluster.ID)
	if err != nil {
		return err
	}

	// If no specific value for setting deletingConfirmationFlag was provided, default to have it enabled so cleanup works as expected.
	if values == nil {
		values = enableDeletionSetting
	} else {
		defaultSettings, ok := values["defaultSettings"].(map[string]any)
		if !ok {
			return errors.New(`Provided values map has invalid value for "defaultSettings"`)
		}

		_, ok = defaultSettings["deletingConfirmationFlag"]
		if !ok {
			defaultSettings["deletingConfirmationFlag"] = true
		}
	}

	chartInstalls := []types.ChartInstall{
		*NewChartInstall(LonghornChartName+"-crd", payload.Version, payload.Cluster.ID, payload.Cluster.Name, payload.Host, catalog.RancherChartRepo, payload.ProjectID, payload.DefaultRegistry, nil),
		*NewChartInstall(LonghornChartName, payload.Version, payload.Cluster.ID, payload.Cluster.Name, payload.Host, catalog.RancherChartRepo, payload.ProjectID, payload.DefaultRegistry, values),
	}

	chartInstallAction := NewChartInstallAction(payload.Namespace, payload.ProjectID, chartInstalls)

	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return UninstallLonghornChart(client, payload.Namespace, payload.Cluster.ID, payload.Host)
	})

	err = shepherdCharts.WaitChartInstall(catalogClient, payload.Namespace, LonghornChartName)
	if err != nil {
		return err
	}

	err = shepherdCharts.WatchAndWaitDeployments(client, payload.Cluster.ID, payload.Namespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	err = shepherdCharts.WatchAndWaitDaemonSets(client, payload.Cluster.ID, payload.Namespace, metav1.ListOptions{})
	return err
}

// UninstallLonghornChart removes Longhorn from the cluster related to the received catalog client object.
func UninstallLonghornChart(client *rancher.Client, namespace string, clusterID string, rancherHostname string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	err = catalogClient.UninstallChart(LonghornChartName, namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	err = waitUninstallation(catalogClient, namespace, LonghornChartName)
	if err != nil {
		return err
	}

	// Uninstall CRDs last so we still have them in case uninstalling longhorn fails as they help debugging.
	err = catalogClient.UninstallChart(LonghornChartName+"-crd", namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	return waitUninstallation(catalogClient, namespace, LonghornChartName+"-crd")
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
			return false, fmt.Errorf("there was an error uninstalling %s chart", chartName)
		case watch.Deleted:
			return true, nil
		}
		return false, nil
	})
}

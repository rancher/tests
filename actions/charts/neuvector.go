package charts

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NeuVectorNamespace = "neuvector-system"
	NeuVectorChartName = "neuvector"
)

// InstallNeuVectorChart installs the NeuVector chart on the cluster according to data on the payload.
// This also waits for installation to complete and checks if the deployments are Ready.
func InstallNeuVectorChart(client *rancher.Client, payload PayloadOpts) error {
	catalogClient, err := client.GetClusterCatalogClient(payload.Cluster.ID)
	if err != nil {
		return err
	}

	chartInstalls := []types.ChartInstall{
		*NewChartInstall(NeuVectorChartName+"-crd", payload.Version, payload.Cluster.ID, payload.Cluster.Name, payload.Host, catalog.RancherChartRepo, payload.ProjectID, payload.DefaultRegistry, nil),
		*NewChartInstall(NeuVectorChartName, payload.Version, payload.Cluster.ID, payload.Cluster.Name, payload.Host, catalog.RancherChartRepo, payload.ProjectID, payload.DefaultRegistry, nil),
	}

	chartInstallAction := NewChartInstallAction(payload.Namespace, payload.ProjectID, chartInstalls)

	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return UninstallNeuVectorChart(client, payload.Namespace, payload.Cluster.ID, payload.Host)
	})

	err = shepherdCharts.WaitChartInstall(catalogClient, payload.Namespace, NeuVectorChartName)
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

// UninstallNeuVectorChart removes NeuVector from the cluster related to the received catalog client object.
func UninstallNeuVectorChart(client *rancher.Client, namespace string, clusterID string, rancherHostname string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	err = catalogClient.UninstallChart(NeuVectorChartName, namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	err = waitUninstallation(catalogClient, namespace, NeuVectorChartName)
	if err != nil {
		return err
	}

	err = catalogClient.UninstallChart(NeuVectorChartName+"-crd", namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	return waitUninstallation(catalogClient, namespace, NeuVectorChartName+"-crd")
}

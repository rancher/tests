package charts

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
)

const (
	NeuVectorNamespace = "neuvector-system"
	NeuVectorChartName = "neuvector"
)

// InstallNeuVectorChart installs the NeuVector chart on the cluster according to data on the payload.
func InstallNeuVectorChart(client *rancher.Client, payload PayloadOpts) error {
	catalogClient, err := client.GetClusterCatalogClient(payload.Cluster.ID)
	if err != nil {
		return err
	}

	chartInstalls := []types.ChartInstall{
		*NewChartInstall(
			NeuVectorChartName+"-crd",
			payload.Version,
			payload.Cluster.ID,
			payload.Cluster.Name,
			payload.Host,
			catalog.RancherChartRepo,
			payload.ProjectID,
			payload.DefaultRegistry,
			nil,
		),
		*NewChartInstall(
			NeuVectorChartName,
			payload.Version,
			payload.Cluster.ID,
			payload.Cluster.Name,
			payload.Host,
			catalog.RancherChartRepo,
			payload.ProjectID,
			payload.DefaultRegistry,
			nil,
		),
		*NewChartInstall(
			NeuVectorChartName+"-monitor",
			payload.Version,
			payload.Cluster.ID,
			payload.Cluster.Name,
			payload.Host,
			catalog.RancherChartRepo,
			payload.ProjectID,
			payload.DefaultRegistry,
			nil,
		),
	}

	chartInstallAction := NewChartInstallAction(payload.Namespace, payload.ProjectID, chartInstalls)
	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return uninstallNeuVectorChart(client, payload.Namespace, payload.Cluster.ID)
	})

	return nil
}

// uninstallNeuVectorChart removes NeuVector from the cluster.
func uninstallNeuVectorChart(client *rancher.Client, namespace string, clusterID string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	err = catalogClient.UninstallChart(NeuVectorChartName+"-monitor", namespace, NewChartUninstallAction())
	if err != nil {
		return err
	}

	err = waitUninstallation(catalogClient, namespace, NeuVectorChartName+"-monitor")
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

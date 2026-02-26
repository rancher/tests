package charts

import (
	"context"
	"fmt"
	"time"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	chartPollInterval = 5 * time.Second
	chartPollTimeout  = 10 * time.Minute
)

// WaitChartDeployed polls the catalog App until it reaches StatusDeployed.
// Unlike watch-based waiting, polling correctly handles the case where the chart
// reaches the desired state before the watch is established.
func WaitChartDeployed(catalogClient *catalog.Client, namespace, chartName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), chartPollInterval, chartPollTimeout, true, func(ctx context.Context) (bool, error) {
		app, err := catalogClient.Apps(namespace).Get(ctx, chartName, metav1.GetOptions{})
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		switch app.Status.Summary.State {
		case string(catalogv1.StatusDeployed):
			return true, nil
		case string(catalogv1.StatusFailed):
			return false, fmt.Errorf("chart %q deploy failed: %s", chartName, app.Status.Summary.Error)
		}
		return false, nil
	})
}

// waitChartGone polls until the named chart App no longer exists.
// Handles the case where the chart was never installed — returns immediately when not found.
func waitChartGone(catalogClient *catalog.Client, namespace, chartName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), chartPollInterval, chartPollTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := catalogClient.Apps(namespace).Get(ctx, chartName, metav1.GetOptions{})
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

// uninstallChartIfPresent uninstalls a chart only when it exists, then waits for removal.
func uninstallChartIfPresent(catalogClient *catalog.Client, namespace, chartName string) error {
	_, err := catalogClient.Apps(namespace).Get(context.TODO(), chartName, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	if err := catalogClient.UninstallChart(chartName, namespace, NewChartUninstallAction()); err != nil {
		return err
	}
	return waitChartGone(catalogClient, namespace, chartName)
}

// InstallLatestNeuVectorChart installs the NeuVector chart matching the current rancher-charts release,
// which deploys only neuvector-crd and neuvector (neuvector-monitor is no longer included).
func InstallLatestNeuVectorChart(client *rancher.Client, payload PayloadOpts) error {
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
	}

	chartInstallAction := NewChartInstallAction(payload.Namespace, payload.ProjectID, chartInstalls)
	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return uninstallLatestNeuVectorChart(client, payload.Namespace, payload.Cluster.ID)
	})

	return nil
}

// uninstallLatestNeuVectorChart removes the neuvector and neuvector-crd charts from the cluster.
// Each chart is only uninstalled if present, preventing cleanup errors when installation failed.
func uninstallLatestNeuVectorChart(client *rancher.Client, namespace string, clusterID string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	if err := uninstallChartIfPresent(catalogClient, namespace, NeuVectorChartName); err != nil {
		return err
	}

	return uninstallChartIfPresent(catalogClient, namespace, NeuVectorChartName+"-crd")
}


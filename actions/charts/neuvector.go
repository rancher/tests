package charts

import (
	"context"
	"time"

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

	NeuVectorNamespace = "cattle-neuvector-system"
	NeuVectorChartName = "neuvector"
)

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

func waitForRancherChartsRepo(client *rancher.Client, clusterID string) error {
	downstreamClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.Background(), chartPollInterval, chartPollTimeout, true, func(ctx context.Context) (bool, error) {
		repo, err := downstreamClient.SteveType(repoType).ByID(catalog.RancherChartRepo)
		if err != nil {
			if k8sErrors.IsNotFound(err) {
				return false, nil
			}

			return false, err
		}

		return repo.State.Name == active, nil
	})
}

// InstallLatestNeuVectorChart installs the latest NeuVector chart (and its CRD chart) on the target cluster using the provided payload options.
func InstallNeuVectorChart(client *rancher.Client, payload PayloadOpts) error {
	if err := waitForRancherChartsRepo(client, payload.Cluster.ID); err != nil {
		return err
	}

	catalogClient, err := client.GetClusterCatalogClient(payload.Cluster.ID)
	if err != nil {
		return err
	}

	chartValues, err := client.Catalog.GetChartValues(catalog.RancherChartRepo, NeuVectorChartName, payload.Version)
	if err != nil {
		return err
	}

	if payload.Hardened {

		enforcer, ok := chartValues["enforcer"].(map[string]interface{})
		if !ok {
			enforcer = map[string]interface{}{}
			chartValues["enforcer"] = enforcer
		}

		enforcer["podAnnotations"] = map[string]interface{}{
			"container.apparmor.security.beta.kubernetes.io/neuvector-enforcer-pod": "unconfined",
		}

		enforcer["securityContext"] = map[string]interface{}{
			"privileged": false,
			"capabilities": map[string]interface{}{
				"add": []string{
					"SYS_ADMIN",
					"NET_ADMIN",
					"SYS_PTRACE",
					"IPC_LOCK",
				},
			},
			"seccompProfile": map[string]interface{}{
				"type": "Unconfined",
			},
		}
	}

	if payload.K3s {
		k3s, ok := chartValues["k3s"].(map[string]interface{})
		if !ok {
			k3s = map[string]interface{}{}
			chartValues["k3s"] = k3s
		}
		k3s["enabled"] = true
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
			chartValues,
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

func uninstallNeuVectorChart(client *rancher.Client, namespace string, clusterID string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	if err := uninstallChartIfPresent(catalogClient, namespace, NeuVectorChartName); err != nil {
		return err
	}

	return uninstallChartIfPresent(catalogClient, namespace, NeuVectorChartName+"-crd")
}

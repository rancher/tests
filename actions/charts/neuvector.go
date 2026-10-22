package charts

import (
	"context"
	"encoding/json"
	"time"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
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

func uninstallChartIfPresent(client *rancher.Client, catalogClient *catalog.Client, opts *PayloadOpts, namespace, chartName string) error {
	_, err := catalogClient.Apps(namespace).Get(context.TODO(), chartName, metav1.GetOptions{})
	if err != nil {
		if k8sErrors.IsNotFound(err) {
			return nil
		}
		return err
	}
	chartOpts := *opts
	chartOpts.Name = chartName
	bodyBytes, err := json.Marshal(NewChartUninstallAction())
	if err != nil {
		return err
	}
	if err := ChartActionWithRetry(context.TODO(), client, verbUninstall, &chartOpts, "", []string{chartName}, buildAppUninstallRequest(catalogClient, namespace, chartName, bodyBytes)); err != nil {
		if k8sErrors.IsNotFound(err) {
			// Chart was removed between the Get and the uninstall call — treat as success.
			return nil
		}
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
	// Callers pass a payload without Name; populate it so chart-action lifecycle and
	// failure logs carry the chart name.
	payload.Name = NeuVectorChartName

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
	bodyBytes, err := json.Marshal(chartInstallAction)
	if err != nil {
		return err
	}
	err = ChartActionWithRetry(context.TODO(), client, verbInstall, &payload, catalog.RancherChartRepo, []string{NeuVectorChartName, NeuVectorChartName + "-crd"}, buildRepoActionRequest(catalogClient, catalog.RancherChartRepo, verbInstall, bodyBytes))
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return uninstallNeuVectorChart(client, payload.Namespace, payload.Cluster.ID)
	})

	return nil
}

// UpgradeNeuVectorChart upgrades the neuvector and neuvector-crd charts to
// installOptions.Version on installOptions.Cluster, waits for the Deployed state
// and for the NeuVector workloads (controller deployment, enforcer daemonset) to
// become Ready again.
//
// The upgrade carries over the values of the installed release (the App resource's
// spec.values, equivalent to helm --reuse-values) instead of applying a blind
// version bump: registry overrides required by airgap environments and the
// enforcer hardening or k3s options set at install time must survive the upgrade,
// or the upgraded NeuVector would come back with chart defaults (docker.io
// images, unhardened enforcer) and break those environments. The neuvector-crd
// chart takes no values, mirroring the install split.
//
// No session cleanup is registered: the uninstall cleanup belongs to the install
// call of the same session, and upgrade-test sessions must not delete the chart.
func UpgradeNeuVectorChart(client *rancher.Client, installOptions *InstallOptions) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	// Reading the installed App yields exactly the values overridden at install
	// time (chart defaults are not stored), which is what must be re-applied.
	installedApp, err := catalogClient.Apps(NeuVectorNamespace).Get(context.TODO(), NeuVectorChartName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	neuVectorChartUpgradeActionPayload := &PayloadOpts{
		InstallOptions:  *installOptions,
		Name:            NeuVectorChartName,
		Namespace:       NeuVectorNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	chartUpgradeAction := newNeuVectorChartUpgradeAction(neuVectorChartUpgradeActionPayload, map[string]interface{}(installedApp.Spec.Values))

	bodyBytes, err := json.Marshal(chartUpgradeAction)
	if err != nil {
		return err
	}
	err = ChartActionWithRetry(context.TODO(), client, verbUpgrade, neuVectorChartUpgradeActionPayload, catalog.RancherChartRepo, []string{NeuVectorChartName, NeuVectorChartName + "-crd"}, buildRepoActionRequest(catalogClient, catalog.RancherChartRepo, verbUpgrade, bodyBytes))
	if err != nil {
		return err
	}

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	if err != nil {
		return err
	}

	adminCatalogClient, err := adminClient.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	watchAppInterface, err := adminCatalogClient.Apps(NeuVectorNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + NeuVectorChartName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusPendingUpgrade) {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return err
	}

	watchAppInterface, err = adminCatalogClient.Apps(NeuVectorNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + NeuVectorChartName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusDeployed) {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return err
	}

	err = shepherdCharts.WatchAndWaitDeployments(client, installOptions.Cluster.ID, NeuVectorNamespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	return shepherdCharts.WatchAndWaitDaemonSets(client, installOptions.Cluster.ID, NeuVectorNamespace, metav1.ListOptions{})
}

// newNeuVectorChartUpgradeAction is a private helper function that returns the
// chart upgrade action for NeuVector, reusing the installed release values on the
// main chart. OpenAPI validation stays enabled: the neuvector CRDs are small and
// do not hit the schema-conflict failures seen on large CRD charts.
func newNeuVectorChartUpgradeAction(p *PayloadOpts, installedValues map[string]interface{}) *types.ChartUpgradeAction {
	chartUpgrade := NewChartUpgrade(p.Name, p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, installedValues)
	chartUpgradeCRD := NewChartUpgrade(p.Name+"-crd", p.Name+"-crd", p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, nil)
	chartUpgrades := []types.ChartUpgrade{*chartUpgradeCRD, *chartUpgrade}

	return NewChartUpgradeAction(p.Namespace, chartUpgrades)
}

func uninstallNeuVectorChart(client *rancher.Client, namespace string, clusterID string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	uninstallPayload := &PayloadOpts{
		InstallOptions: InstallOptions{Cluster: &clusters.ClusterMeta{ID: clusterID}},
		Name:           NeuVectorChartName,
		Namespace:      namespace,
	}

	if err := uninstallChartIfPresent(client, catalogClient, uninstallPayload, namespace, NeuVectorChartName); err != nil {
		return err
	}

	return uninstallChartIfPresent(client, catalogClient, uninstallPayload, namespace, NeuVectorChartName+"-crd")
}

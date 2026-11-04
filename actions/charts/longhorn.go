package charts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/rancher/tests/actions/namespaces"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

var (
	LonghornStorageClass       = "longhorn"
	LonghornStaticStorageClass = "longhorn-static"
	LonghornNamespace          = "longhorn-system"
	LonghornChartName          = "longhorn"
	enableDeletionSetting      = map[string]any{
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
	// Callers pass a payload without Name; populate it so chart-action lifecycle and
	// failure logs carry the chart name.
	payload.Name = LonghornChartName

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

	bodyBytes, err := json.Marshal(chartInstallAction)
	if err != nil {
		return err
	}
	err = ChartActionWithRetry(context.TODO(), client, verbInstall, &payload, catalog.RancherChartRepo, []string{LonghornChartName, LonghornChartName + "-crd"}, buildRepoActionRequest(catalogClient, catalog.RancherChartRepo, verbInstall, bodyBytes))
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

// UpgradeLonghornChart upgrades the longhorn and longhorn-crd charts to
// installOptions.Version on installOptions.Cluster and waits for Deployed state.
// No session cleanup is registered: the uninstall cleanup belongs to the install
// call of the same session, and upgrade-test sessions must not delete the chart.
func UpgradeLonghornChart(client *rancher.Client, installOptions *InstallOptions) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	longhornChartUpgradeActionPayload := &PayloadOpts{
		InstallOptions:  *installOptions,
		Name:            LonghornChartName,
		Namespace:       LonghornNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	chartUpgradeAction := newLonghornChartUpgradeAction(longhornChartUpgradeActionPayload)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	bodyBytes, err := json.Marshal(chartUpgradeAction)
	if err != nil {
		return err
	}
	err = ChartActionWithRetry(context.TODO(), client, verbUpgrade, longhornChartUpgradeActionPayload, catalog.RancherChartRepo, []string{LonghornChartName, LonghornChartName + "-crd"}, buildRepoActionRequest(catalogClient, catalog.RancherChartRepo, verbUpgrade, bodyBytes))
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

	watchAppInterface, err := adminCatalogClient.Apps(LonghornNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + LonghornChartName,
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

	watchAppInterface, err = adminCatalogClient.Apps(LonghornNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + LonghornChartName,
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

	err = shepherdCharts.WatchAndWaitDeployments(client, installOptions.Cluster.ID, LonghornNamespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	return shepherdCharts.WatchAndWaitDaemonSets(client, installOptions.Cluster.ID, LonghornNamespace, metav1.ListOptions{})
}

// newLonghornChartUpgradeAction is a private helper function that returns the chart upgrade action for Longhorn.
func newLonghornChartUpgradeAction(p *PayloadOpts) *types.ChartUpgradeAction {
	// Pass the deletingConfirmationFlag value on the main chart so the upgrade does
	// not reset it; the CRD chart takes no Longhorn settings (same split as install).
	chartUpgrade := NewChartUpgrade(p.Name, p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, enableDeletionSetting)
	chartUpgradeCRD := NewChartUpgrade(p.Name+"-crd", p.Name+"-crd", p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, nil)
	chartUpgrades := []types.ChartUpgrade{*chartUpgradeCRD, *chartUpgrade}

	chartUpgradeAction := NewChartUpgradeAction(p.Namespace, chartUpgrades)
	// Disable OpenAPI validation to avoid schema-conflict failures on the large
	// Longhorn CRDs, which frequently change shape between versions.
	chartUpgradeAction.DisableOpenAPIValidation = true

	return chartUpgradeAction
}

// UninstallLonghornChart removes Longhorn from the cluster related to the received catalog client object.
func UninstallLonghornChart(client *rancher.Client, namespace string, clusterID string, rancherHostname string) error {
	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	uninstallPayload := &PayloadOpts{
		InstallOptions: InstallOptions{Cluster: &clusters.ClusterMeta{ID: clusterID}},
		Name:           LonghornChartName,
		Namespace:      namespace,
		Host:           rancherHostname,
	}

	bodyBytes, err := json.Marshal(NewChartUninstallAction())
	if err != nil {
		return err
	}
	err = ChartActionWithRetry(context.TODO(), client, verbUninstall, uninstallPayload, "", []string{LonghornChartName}, buildAppUninstallRequest(catalogClient, namespace, LonghornChartName, bodyBytes))
	if err != nil {
		return err
	}

	err = waitUninstallation(catalogClient, namespace, LonghornChartName)
	if err != nil {
		return err
	}

	// Uninstall CRDs last so we still have them in case uninstalling longhorn fails as they help debugging.
	crdBodyBytes, err := json.Marshal(NewChartUninstallAction())
	if err != nil {
		return err
	}
	err = ChartActionWithRetry(context.TODO(), client, verbUninstall, uninstallPayload, "", []string{LonghornChartName + "-crd"}, buildAppUninstallRequest(catalogClient, namespace, LonghornChartName+"-crd", crdBodyBytes))
	if err != nil {
		return err
	}

	err = waitUninstallation(catalogClient, namespace, LonghornChartName+"-crd")
	if err != nil {
		return err
	}

	// remove the longhorn namespace that was created
	steveAdminClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	namespaceObject, err := steveAdminClient.SteveType(namespaces.NamespaceSteveType).ByID(namespace)
	if err != nil {
		return err
	}

	return steveAdminClient.SteveType(namespaces.NamespaceSteveType).Delete(namespaceObject)
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

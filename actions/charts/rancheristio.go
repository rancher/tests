package charts

import (
	"context"
	"fmt"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	// Namespace that rancher istio chart is installed in
	RancherIstioNamespace = "istio-system"
	// Name of the rancher istio chart
	RancherIstioName = "rancher-istio"
)

// InstallRancherIstioChart is a helper function that installs the rancher-istio chart.
func InstallRancherIstioChart(client *rancher.Client, installOptions *InstallOptions, rancherIstioOpts *RancherIstioOpts) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	istioChartInstallActionPayload := &PayloadOpts{
		InstallOptions:  *installOptions,
		Name:            RancherIstioName,
		Namespace:       RancherIstioNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	chartInstallAction := newIstioChartInstallAction(istioChartInstallActionPayload, rancherIstioOpts)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	// Cleanup registration
	client.Session.RegisterCleanupFunc(func() error {
		// UninstallAction for when uninstalling the rancher-istio chart
		defaultChartUninstallAction := NewChartUninstallAction()

		err := catalogClient.UninstallChart(RancherIstioName, RancherIstioNamespace, defaultChartUninstallAction)
		if err != nil {
			return err
		}

		watchAppInterface, err := catalogClient.Apps(RancherIstioNamespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + RancherIstioName,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error uninstalling rancher istio chart")
			} else if event.Type == watch.Deleted {
				return true, nil
			}
			return false, nil
		})

		return err
	})

	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	// wait for chart to be full deployed
	watchAppInterface, err := catalogClient.Apps(RancherIstioNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + RancherIstioName,
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
	return nil
}

// newIstioChartInstallAction is a private helper function that returns chart install action with istio and payload options.
func newIstioChartInstallAction(p *PayloadOpts, rancherIstioOpts *RancherIstioOpts) *types.ChartInstallAction {
	istioValues := map[string]interface{}{
		"tracing": map[string]interface{}{
			"enabled": rancherIstioOpts.Tracing,
		},
		"kiali": map[string]interface{}{
			"enabled": rancherIstioOpts.Kiali,
		},
		"ingressGateways": map[string]interface{}{
			"enabled": rancherIstioOpts.IngressGateways,
		},
		"egressGateways": map[string]interface{}{
			"enabled": rancherIstioOpts.EgressGateways,
		},
		"pilot": map[string]interface{}{
			"enabled": rancherIstioOpts.Pilot,
		},
		"telemetry": map[string]interface{}{
			"enabled": rancherIstioOpts.Telemetry,
		},
		"cni": map[string]interface{}{
			"enabled": rancherIstioOpts.CNI,
		},
	}
	chartInstall := NewChartInstall(p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, rancherChartsName, p.ProjectID, p.DefaultRegistry, istioValues)
	chartInstalls := []types.ChartInstall{*chartInstall}

	chartInstallAction := NewChartInstallAction(p.Namespace, p.ProjectID, chartInstalls)

	return chartInstallAction
}

// UpgradeRancherIstioChart is a helper function that upgrades the rancher-istio chart.
func UpgradeRancherIstioChart(client *rancher.Client, installOptions *InstallOptions, rancherIstioOpts *RancherIstioOpts) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	istioChartUpgradeActionPayload := &PayloadOpts{
		InstallOptions:  *installOptions,
		Name:            RancherIstioName,
		Namespace:       RancherIstioNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	chartUpgradeAction := newIstioChartUpgradeAction(istioChartUpgradeActionPayload, rancherIstioOpts)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	err = catalogClient.UpgradeChart(chartUpgradeAction, catalog.RancherChartRepo)
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

	// wait for chart to be in status pending upgrade
	watchAppInterface, err := adminCatalogClient.Apps(RancherIstioNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + RancherIstioName,
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

	// wait for chart to be full deployed
	watchAppInterface, err = adminCatalogClient.Apps(RancherIstioNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + RancherIstioName,
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

	return nil
}

// newIstioChartUpgradeAction is a private helper function that returns chart upgrade action with istio and payload options.
func newIstioChartUpgradeAction(p *PayloadOpts, rancherIstioOpts *RancherIstioOpts) *types.ChartUpgradeAction {
	istioValues := map[string]interface{}{
		"tracing": map[string]interface{}{
			"enabled": rancherIstioOpts.Tracing,
		},
		"kiali": map[string]interface{}{
			"enabled": rancherIstioOpts.Kiali,
		},
		"ingressGateways": map[string]interface{}{
			"enabled": rancherIstioOpts.IngressGateways,
		},
		"egressGateways": map[string]interface{}{
			"enabled": rancherIstioOpts.EgressGateways,
		},
		"pilot": map[string]interface{}{
			"enabled": rancherIstioOpts.Pilot,
		},
		"telemetry": map[string]interface{}{
			"enabled": rancherIstioOpts.Telemetry,
		},
		"cni": map[string]interface{}{
			"enabled": rancherIstioOpts.CNI,
		},
	}
	chartUpgrade := NewChartUpgrade(p.Name, p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, istioValues)
	chartUpgrades := []types.ChartUpgrade{*chartUpgrade}

	chartUpgradeAction := NewChartUpgradeAction(p.Namespace, chartUpgrades)

	return chartUpgradeAction
}

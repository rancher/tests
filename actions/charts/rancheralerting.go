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
	// Namespace that rancher alerting drivers chart is installed
	RancherAlertingNamespace = RancherMonitoringNamespace
	// Name of the rancher alerting drivers chart
	RancherAlertingName = "rancher-alerting-drivers"
)

// InstallRancherALertingChart is a helper function that installs the rancher-alerting-drivers chart.
func InstallRancherAlertingChart(client *rancher.Client, installOptions *InstallOptions, rancherAlertingOpts *RancherAlertingOpts) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	alertingChartInstallActionPayload := &PayloadOpts{
		InstallOptions:  *installOptions,
		Name:            RancherAlertingName,
		Namespace:       RancherAlertingNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	chartInstallAction := newAlertingChartInstallAction(alertingChartInstallActionPayload, rancherAlertingOpts)
	if err != nil {
		return err
	}

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	// Cleanup registration
	client.Session.RegisterCleanupFunc(func() error {
		// UninstallAction for when uninstalling the rancher-alerting-drivers chart
		defaultChartUninstallAction := NewChartUninstallAction()

		err = catalogClient.UninstallChart(RancherAlertingName, RancherAlertingNamespace, defaultChartUninstallAction)
		if err != nil {
			return err
		}

		watchAppInterface, err := catalogClient.Apps(RancherAlertingNamespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + RancherAlertingName,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error uninstalling rancher alert drivers chart")
			} else if event.Type == watch.Deleted {
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return err
		}

		return nil
	})

	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	// wait for chart to be full deployed
	watchAppInterface, err := catalogClient.Apps(RancherAlertingNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + RancherAlertingName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	return wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusDeployed) {
			return true, nil
		}
		return false, nil
	})
}

func newAlertingChartInstallAction(p *PayloadOpts, opts *RancherAlertingOpts) *types.ChartInstallAction {
	alertingValues := map[string]interface{}{
		"prom2teams": map[string]interface{}{
			"enabled": opts.Teams,
		},
		"sachet": map[string]interface{}{
			"enabled": opts.SMS,
		},
	}

	chartInstall := NewChartInstall(p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, rancherChartsName, p.ProjectID, p.DefaultRegistry, alertingValues)
	chartInstalls := []types.ChartInstall{*chartInstall}

	return NewChartInstallAction(p.Namespace, p.ProjectID, chartInstalls)
}

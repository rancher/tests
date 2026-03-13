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
	kubenamespaces "github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/namespaces"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	ComplianceNamespace = "compliance-operator-system"
	ComplianceName      = "rancher-compliance"
)

// InstallComplianceChart is a helper function that installs the rancher-compliance chart.
func InstallComplianceChart(client *rancher.Client, ChartInstallActionPayload *PayloadOpts) error {
	chartInstallAction, err := newRancherComplianceChartInstallAction(ChartInstallActionPayload)
	if err != nil {
		return err
	}

	catalogClient, err := client.GetClusterCatalogClient(ChartInstallActionPayload.InstallOptions.Cluster.ID)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		defaultChartUninstallAction := NewChartUninstallAction()

		err = catalogClient.UninstallChart(ChartInstallActionPayload.Name, ChartInstallActionPayload.Namespace, defaultChartUninstallAction)
		if err != nil {
			return err
		}

		watchAppInterface, err := catalogClient.Apps(ChartInstallActionPayload.Namespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + ChartInstallActionPayload.Name,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			switch event.Type {
			case watch.Error:
				return false, fmt.Errorf("there was an error uninstalling Rancher Compliance chart")
			case watch.Deleted:
				return true, nil
			}

			return false, nil
		})
		if err != nil {
			return err
		}

		err = catalogClient.UninstallChart(ChartInstallActionPayload.Name+"-crd", ChartInstallActionPayload.Name, defaultChartUninstallAction)
		if err != nil {
			return err
		}

		watchAppInterface, err = catalogClient.Apps(ChartInstallActionPayload.Namespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + ChartInstallActionPayload.Name,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			chart := event.Object.(*catalogv1.App)
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error uninstalling Rancher Compliance chart")
			} else if event.Type == watch.Deleted {
				return true, nil
			} else if chart == nil {
				return true, nil
			}

			return false, nil
		})
		if err != nil {
			return err
		}

		steveclient, err := client.Steve.ProxyDownstream(ChartInstallActionPayload.InstallOptions.Cluster.ID)
		if err != nil {
			return err
		}

		namespaceClient := steveclient.SteveType(namespaces.NamespaceSteveType)

		namespace, err := namespaceClient.ByID(ChartInstallActionPayload.Namespace)
		if err != nil {
			return err
		}

		err = namespaceClient.Delete(namespace)
		if err != nil {
			return err
		}

		adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
		if err != nil {
			return err
		}

		adminDynamicClient, err := adminClient.GetDownStreamClusterClient(ChartInstallActionPayload.InstallOptions.Cluster.ID)
		if err != nil {
			return err
		}

		adminNamespaceResource := adminDynamicClient.Resource(kubenamespaces.NamespaceGroupVersionResource).Namespace("")

		watchNamespaceInterface, err := adminNamespaceResource.Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + ChartInstallActionPayload.Namespace,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		return wait.WatchWait(watchNamespaceInterface, func(event watch.Event) (ready bool, err error) {
			if event.Type == watch.Deleted {
				return true, nil
			}

			return false, nil
		})
	})

	err = catalogClient.InstallChart(chartInstallAction, catalog.RancherChartRepo)
	if err != nil {
		return err
	}

	watchAppInterface, err := catalogClient.Apps(ChartInstallActionPayload.Namespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + ChartInstallActionPayload.Name,
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

// newRancherComplianceChartInstallAction is a private helper function that returns chart install action with Rancher
// compliance and payload options.
func newRancherComplianceChartInstallAction(p *PayloadOpts) (*types.ChartInstallAction, error) {
	chartInstall := NewChartInstall(p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, rancherChartsName, p.ProjectID, p.DefaultRegistry, nil)
	chartInstallCRD := NewChartInstall(p.Name+"-crd", p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, rancherChartsName, p.ProjectID, p.DefaultRegistry, nil)
	chartInstalls := []types.ChartInstall{*chartInstallCRD, *chartInstall}

	chartInstallAction := NewChartInstallAction(p.Namespace, p.ProjectID, chartInstalls)

	return chartInstallAction, nil
}

// UpgradeRancherComplianceChart is a helper function that upgrades the rancher-compliance chart.
func UpgradeRancherComplianceChart(client *rancher.Client, installOptions *InstallOptions) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	registrySetting, err := client.Management.Setting.ByID(defaultRegistrySettingID)
	if err != nil {
		return err
	}

	benchmarkChartUpgradeActionPayload := &PayloadOpts{
		InstallOptions:  *installOptions,
		Name:            ComplianceName,
		Namespace:       ComplianceNamespace,
		Host:            serverSetting.Value,
		DefaultRegistry: registrySetting.Value,
	}

	chartUpgradeAction := newRancherComplianceChartUpgradeAction(benchmarkChartUpgradeActionPayload)

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

	watchAppInterface, err := adminCatalogClient.Apps(ComplianceNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + ComplianceName,
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

	watchAppInterface, err = adminCatalogClient.Apps(ComplianceNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + ComplianceName,
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

// newRancherComplianceChartUpgradeAction is a private helper function that returns chart upgrade action.
func newRancherComplianceChartUpgradeAction(p *PayloadOpts) *types.ChartUpgradeAction {
	chartUpgrade := NewChartUpgrade(p.Name, p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, nil)
	chartUpgradeCRD := NewChartUpgrade(p.Name+"-crd", p.Name+"-crd", p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, nil)
	chartUpgrades := []types.ChartUpgrade{*chartUpgradeCRD, *chartUpgrade}

	chartUpgradeAction := NewChartUpgradeAction(p.Namespace, chartUpgrades)

	return chartUpgradeAction
}

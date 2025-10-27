package uiplugins

import (
	"context"
	"fmt"

	v1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	extensionNamespace = "cattle-ui-plugin-system"
)

// newUIPluginInstallAction is a private helper function that returns chart install action with the extension payload options.
func newUIPluginInstallAction(p *ExtensionOptions) *types.ChartInstallAction {

	chartInstall := newPluginsInstall(p.ChartName, p.Version, nil)
	chartInstalls := []types.ChartInstall{*chartInstall}

	chartInstallAction := &types.ChartInstallAction{
		Namespace: extensionNamespace,
		Charts:    chartInstalls,
	}

	return chartInstallAction
}

// InstallUIPlugin is a helper function that installs a UI extension chart in the local cluster of rancher.
func InstallUIPlugin(client *rancher.Client, installExtensionOptions *ExtensionOptions, chartRepoName string) error {
	extensionInstallAction := newUIPluginInstallAction(installExtensionOptions)

	catalogClient, err := client.GetClusterCatalogClient(local)
	if err != nil {
		return err
	}

	client.Session.RegisterCleanupFunc(func() error {
		defaultChartUninstallAction := newPluginUninstallAction()

		err := catalogClient.UninstallChart(installExtensionOptions.ChartName, extensionNamespace, defaultChartUninstallAction)
		if err != nil {
			return err
		}

		watchAppInterface, err := catalogClient.Apps(extensionNamespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + installExtensionOptions.ChartName,
			TimeoutSeconds: &timeoutSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			chart := event.Object.(*v1.App)
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error uninstalling %s extension", installExtensionOptions.ChartName)
			} else if event.Type == watch.Deleted {
				logrus.Infof("Uninstalled %s extension successfully.", installExtensionOptions)
				return true, nil
			} else if chart == nil {
				return true, nil
			}
			return false, nil

		})

		return err

	})
	err = catalogClient.InstallChart(extensionInstallAction, chartRepoName)
	if err != nil {
		return err
	}

	watchAppInterface, err := catalogClient.Apps(extensionNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + installExtensionOptions.ChartName,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*v1.App)

		state := app.Status.Summary.State
		if state == string(v1.StatusDeployed) {
			return true, nil
		}
		return false, nil
	})

	return err
}

// CreateExtensionsRepo is a helper that utilizes the rancher client and add the ui extensions repo to the list if repositories in the local cluster.
func CreateExtensionsRepo(client *rancher.Client, rancherUiPluginsName, uiExtensionGitRepoURL, uiExtensionsRepoBranch string) error {
	logrus.Info("Adding ui extensions repo to rancher chart repositories in the local cluster.")

	clusterRepoObj := v1.ClusterRepo{
		ObjectMeta: metav1.ObjectMeta{
			Name: rancherUiPluginsName,
		},
		Spec: v1.RepoSpec{
			GitRepo:   uiExtensionGitRepoURL,
			GitBranch: uiExtensionsRepoBranch,
		},
	}

	repoObject, err := client.Catalog.ClusterRepos().Create(context.TODO(), &clusterRepoObj, metav1.CreateOptions{})
	if err != nil {
		return err
	}
	client.Session.RegisterCleanupFunc(func() error {
		err := client.Catalog.ClusterRepos().Delete(context.TODO(), repoObject.Name, metav1.DeleteOptions{})
		if err != nil {
			return err
		}

		watchAppInterface, err := client.Catalog.ClusterRepos().Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + repoObject.Name,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error deleting the cluster repo")
			} else if event.Type == watch.Deleted {
				logrus.Info("Removed extensions repo successfully.")
				return true, nil
			}
			return false, nil
		})

		return err
	})

	watchAppInterface, err := client.Catalog.ClusterRepos().Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + clusterRepoObj.Name,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})

	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		repo := event.Object.(*v1.ClusterRepo)

		state := repo.Status.Conditions
		for _, condition := range state {
			if condition.Type == string(v1.RepoDownloaded) && condition.Status == "True" {
				return true, nil
			}
		}
		return false, nil
	})

	return err
}

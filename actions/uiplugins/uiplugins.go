package uiplugins

import (
	"context"
	"time"

	v1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/sirupsen/logrus"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	extensionNamespace = "cattle-ui-plugin-system"
	pollInterval       = 5 * time.Second
	pollTimeout        = 2 * time.Minute
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
			if k8sErrors.IsNotFound(err) {
				// Extension was already removed — nothing to clean up.
				return nil
			}
			return err
		}

		// Poll until the extension App is gone. A watch started after
		// UninstallChart can miss the Deleted event when the uninstall
		// completes quickly (same race as the repo-delete cleanup).
		return kwait.PollUntilContextTimeout(context.Background(), pollInterval, pollTimeout, true, func(ctx context.Context) (bool, error) {
			_, getErr := catalogClient.Apps(extensionNamespace).Get(ctx, installExtensionOptions.ChartName, metav1.GetOptions{})
			if k8sErrors.IsNotFound(getErr) {
				logrus.Infof("Uninstalled %s extension successfully.", installExtensionOptions.ChartName)
				return true, nil
			}
			return false, getErr
		})

	})
	err = installChartWithRetry(catalogClient, extensionInstallAction, chartRepoName, installExtensionOptions.ChartName)
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

	if err != nil {
		logrus.Warnf("Watch for UI plugin %s install ended with error (%v); verifying current app state...", installExtensionOptions.ChartName, err)
		err = charts.WaitChartInstall(catalogClient, extensionNamespace, installExtensionOptions.ChartName)
		if err != nil {
			return err
		}
	}

	return nil
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
			if k8sErrors.IsNotFound(err) {
				// Repo was already removed — nothing to clean up.
				return nil
			}
			return err
		}

		// Poll until the repo is gone. A watch started after Delete can miss the
		// Deleted event because the resource is removed synchronously before the
		// watch connects, causing it to block until the 30-minute watch timeout.
		return kwait.PollUntilContextTimeout(context.Background(), pollInterval, pollTimeout, true, func(ctx context.Context) (bool, error) {
			_, getErr := client.Catalog.ClusterRepos().Get(ctx, repoObject.Name, metav1.GetOptions{})
			if k8sErrors.IsNotFound(getErr) {
				logrus.Info("Removed extensions repo successfully.")
				return true, nil
			}
			return false, getErr
		})
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

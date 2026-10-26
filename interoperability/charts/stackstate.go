package charts

import (
	"context"
	"fmt"
	"time"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	rv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	"github.com/rancher/shepherd/pkg/api/steve/catalog/types"
	"github.com/rancher/shepherd/pkg/wait"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/interoperability/observability"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
)

const (
	// clusterRepoCreateRetries bounds the create attempts for a ClusterRepo on
	// a freshly settled management cluster.
	clusterRepoCreateRetries = 4
	// clusterRepoCreateRetryDelay is the spacing between ClusterRepo create attempts.
	clusterRepoCreateRetryDelay = 15 * time.Second

	// chartActionRetryAttempts bounds the attempts for a chart install or
	// upgrade action POST.
	chartActionRetryAttempts = 3
	// chartActionRetryDelay is the spacing between chart action attempts,
	// giving the management cluster time to finish and cache a large chart's
	// first render.
	chartActionRetryDelay = 30 * time.Second

	// stackstateHelmActionTimeout is the helm timeout carried by the chart
	// actions. The default ten minutes does not cover a cold cluster pulling
	// the full SUSE Observability image set: build 35's install hit the helm
	// deadline with workloads still pulling, and the app failed.
	stackstateHelmActionTimeout = 30 * time.Minute
	// chartTemplatePollInterval and chartTemplatePollTimeout bound the wait
	// for a fresh ClusterRepo's chart template to be fetched and cached
	// server-side; the template requires downloading the chart tarball, which
	// can span several request windows for large prime charts.
	chartTemplatePollInterval = 10 * time.Second
	chartTemplatePollTimeout  = 10 * time.Minute
)

// appWatchSeconds bounds the app watches: it must exceed the helm action
// timeout comfortably, or the watch gives up on an install that is still
// running healthily (build 36's watch tied with the thirty-minute helm
// deadline and lost by seconds).
var appWatchSeconds = int64((stackstateHelmActionTimeout + 10*time.Minute).Seconds())

// WaitForChartTemplate polls the chart template info link until the catalog
// has fetched and cached the chart version template server-side. Fresh
// ClusterRepos resolve their version list quickly, but the per-version
// template needs the chart tarball, which large prime charts download across
// several request windows; until the template is cached, chart action POSTs
// resolve it synchronously and can outlive the request deadline, answering
// with a bare internal error.
func WaitForChartTemplate(catalogClient *catalog.Client, repoName, chartName, chartVersion string) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), chartTemplatePollInterval, chartTemplatePollTimeout, true, func(context.Context) (bool, error) {
		_, err := catalogClient.GetChartValues(repoName, chartName, chartVersion)
		return err == nil, nil
	})
	if err != nil {
		return fmt.Errorf("chart template %s %s from repo %s did not become available within %s: %w", chartName, chartVersion, repoName, chartTemplatePollTimeout, err)
	}
	return nil
}

// retryChartAction runs a chart install or upgrade action POST with bounded
// retries. The suse-observability charts are large: their first action can
// outlive the Rancher proxy's request timeout while templates render and
// cache, answering with a bare internal error. An attempt that persisted
// server-side despite its error surfaces as an already-exists conflict, which
// satisfies the action.
func retryChartAction(action string, fn func() error) error {
	var lastErr error
	for attempt := 1; attempt <= chartActionRetryAttempts; attempt++ {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if k8sErrors.IsAlreadyExists(lastErr) || k8sErrors.IsConflict(lastErr) {
			log.Infof("%s already exists after a failed attempt; treating as applied", action)
			return nil
		}
		log.Warnf("%s attempt %d/%d failed: %v", action, attempt, chartActionRetryAttempts, lastErr)
		if attempt < chartActionRetryAttempts {
			// Spacing between attempts: with immediate=false the first condition
			// check runs only after the interval, so this poll acts as the
			// context-aware sleep. Its error is ignored because the condition
			// succeeds at that first check (the timeout outlives the interval
			// and the context is never cancelled).
			_ = kwait.PollUntilContextTimeout(context.TODO(), chartActionRetryDelay, chartActionRetryDelay+time.Second, false, func(context.Context) (bool, error) {
				return true, nil
			})
		}
	}
	return fmt.Errorf("%s: %w", action, lastErr)
}

// InstallStackStateServerChart installs the StackState chart into the specified Kubernetes cluster and namespace.
// It uses Rancher client and configuration details, and waits for the chart deployment to complete successfully.
// Parameters:
// - client: the Rancher client used for connecting to the Kubernetes cluster.
// - installOptions: the installation options including cluster info and chart version.
// - stackstateConfigs: configuration details for StackState such as the service token and URL.
// - systemProjectID: the ID of the system project where the chart is deployed.
// - additionalValues: additional Helm chart values as a map for custom configurations.
// Returns an error if the chart installation fails or its status cannot be confirmed.
func InstallStackStateServerChart(client *rancher.Client, installOptions *charts.InstallOptions, systemProjectID string, additionalValues map[string]interface{}) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		log.Info("Error getting server setting.")
		return err
	}

	stackstateChartInstallActionPayload := &charts.PayloadOpts{
		InstallOptions: *installOptions,
		Name:           StackStateServerChartRepo,
		Namespace:      StackStateServerNamespace,
		Host:           serverSetting.Value,
	}

	chartInstallAction := newStackStateServerChartInstallAction(stackstateChartInstallActionPayload, systemProjectID, additionalValues)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		log.Info("Error getting catalogClient")
		return err
	}

	err = retryChartAction("suse-observability server chart install", func() error {
		return catalogClient.InstallChart(chartInstallAction, StackStateServerChartRepo)
	})
	if err != nil {
		return err
	}

	watchAppInterface, err := catalogClient.Apps(StackStateServerNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + StackStateServerChartRepo,
		TimeoutSeconds: &appWatchSeconds,
	})
	if err != nil {
		log.Info("StackState App failed to install")
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusFailed) {
			return false, fmt.Errorf("app %s entered failed state", app.Name)
		}
		if state == string(catalogv1.StatusDeployed) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		log.Info("Unable to obtain the status of the installed app ")
		return err
	}
	return nil
}

// InstallStackstateAgentChart is a private helper function that returns chart install action with stack state agent and payload options.
func InstallStackstateAgentChart(client *rancher.Client, installOptions *charts.InstallOptions, stackstateConfigs *observability.StackStateConfig, systemProjectID string) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		log.Info("Error getting server setting.")
		return err
	}

	stackstateAgentChartInstallActionPayload := &charts.PayloadOpts{
		InstallOptions: *installOptions,
		Name:           StackstateK8sAgent,
		Namespace:      StackstateNamespace,
		Host:           serverSetting.Value,
	}

	chartInstallAction := newStackstateAgentChartInstallAction(stackstateAgentChartInstallActionPayload, stackstateConfigs, systemProjectID)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		log.Info("Error getting catalogClient")
		return err
	}

	// register uninstall stackstate agent as a cleanup function
	client.Session.RegisterCleanupFunc(func() error {
		defaultChartUninstallAction := charts.NewChartUninstallAction()

		err := catalogClient.UninstallChart(StackstateK8sAgent, StackstateNamespace, defaultChartUninstallAction)
		if err != nil {
			return err
		}

		watchAppInterface, err := catalogClient.Apps(StackstateNamespace).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + StackstateK8sAgent,
			TimeoutSeconds: &appWatchSeconds,
		})
		if err != nil {
			return err
		}

		err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
			chart := event.Object.(*catalogv1.App)
			if event.Type == watch.Error {
				return false, fmt.Errorf("there was an error uninstalling stackstate agent chart")
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

		steveclient, err := client.Steve.ProxyDownstream(installOptions.Cluster.ID)
		if err != nil {
			return err
		}
		namespaceClient := steveclient.SteveType(namespaces.NamespaceSteveType)

		namespace, err := namespaceClient.ByID(StackstateNamespace)
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

		adminDynamicClient, err := adminClient.GetDownStreamClusterClient(installOptions.Cluster.ID)
		if err != nil {
			return err
		}

		adminNamespaceResource := adminDynamicClient.Resource(namespaces.NamespaceGroupVersionResource).Namespace("")

		watchNamespaceInterface, err := adminNamespaceResource.Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + StackstateNamespace,
			TimeoutSeconds: &appWatchSeconds,
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

	err = retryChartAction("stackstate-k8s-agent chart install", func() error {
		return catalogClient.InstallChart(chartInstallAction, RancherPartnerChartRepo)
	})
	if err != nil {
		return err
	}

	// wait for chart to be fully deployed
	watchAppInterface, err := catalogClient.Apps(StackstateNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + StackstateK8sAgent,
		TimeoutSeconds: &appWatchSeconds,
	})
	if err != nil {
		log.Info("Unable to obtain the installed app ")
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusFailed) {
			return false, fmt.Errorf("app %s entered failed state", app.Name)
		}
		if state == string(catalogv1.StatusDeployed) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		log.Info("Unable to obtain the status of the installed app ")
		return err
	}
	return nil
}

// newStackstateAgentChartInstallAction is a helper function that returns an array of charts.NewChartInstallActions for installing the stackstate agent charts
func newStackstateAgentChartInstallAction(p *charts.PayloadOpts, stackstateConfigs *observability.StackStateConfig, systemProjectID string) *types.ChartInstallAction {
	stackstateValues := map[string]interface{}{
		"stackstate": map[string]interface{}{
			"cluster": map[string]interface{}{
				"name": p.Cluster.Name,
			},
			"apiKey": stackstateConfigs.ClusterApiKey,
			"url":    stackstateConfigs.Url,
		},
	}

	chartInstall := charts.NewChartInstall(p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, rancherPartnerCharts, systemProjectID, p.DefaultRegistry, stackstateValues)

	chartInstalls := []types.ChartInstall{*chartInstall}
	chartInstallAction := charts.NewChartInstallAction(p.Namespace, p.ProjectID, chartInstalls)
	chartInstallAction.Timeout = &metav1.Duration{Duration: stackstateHelmActionTimeout}

	return chartInstallAction
}

func newStackStateServerChartInstallAction(p *charts.PayloadOpts, systemProjectID string, additionalValues map[string]interface{}) *types.ChartInstallAction {
	chartInstall := charts.NewChartInstall(p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, StackStateServerChartRepo, systemProjectID, p.DefaultRegistry, additionalValues)

	chartInstalls := []types.ChartInstall{*chartInstall}
	chartInstallAction := charts.NewChartInstallAction(p.Namespace, p.ProjectID, chartInstalls)
	chartInstallAction.Timeout = &metav1.Duration{Duration: stackstateHelmActionTimeout}

	return chartInstallAction
}

// UpgradeStackstateAgentChart is a helper function that upgrades the stackstate agent chart.
func UpgradeStackstateAgentChart(client *rancher.Client, installOptions *charts.InstallOptions, stackstateConfigs *observability.StackStateConfig, systemProjectID string) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	stackstateAgentChartUpgradeActionPayload := &charts.PayloadOpts{
		InstallOptions: *installOptions,
		Name:           StackstateK8sAgent,
		Namespace:      StackstateNamespace,
		Host:           serverSetting.Value,
	}

	chartUpgradeAction := newStackstateAgentChartUpgradeAction(stackstateAgentChartUpgradeActionPayload, stackstateConfigs)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	err = retryChartAction("stackstate-k8s-agent chart upgrade", func() error {
		return catalogClient.UpgradeChart(chartUpgradeAction, RancherPartnerChartRepo)
	})
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
	watchAppInterface, err := adminCatalogClient.Apps(StackstateNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + StackstateK8sAgent,
		TimeoutSeconds: &appWatchSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusFailed) {
			return false, fmt.Errorf("app %s entered failed state", app.Name)
		}
		if state == string(catalogv1.StatusPendingUpgrade) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}

	watchAppInterface, err = adminCatalogClient.Apps(StackstateNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + StackstateK8sAgent,
		TimeoutSeconds: &appWatchSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusFailed) {
			return false, fmt.Errorf("app %s entered failed state", app.Name)
		}
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

// newStackstateAgentChartUpgradeAction is a private helper function that returns chart upgrade action.
func newStackstateAgentChartUpgradeAction(p *charts.PayloadOpts, stackstateConfigs *observability.StackStateConfig) *types.ChartUpgradeAction {
	stackstateValues := map[string]interface{}{
		"stackstate": map[string]interface{}{
			"cluster": map[string]interface{}{
				"name": p.Cluster.Name,
			},
			"apiKey": stackstateConfigs.ClusterApiKey,
			"url":    stackstateConfigs.Url,
		},
	}

	chartUpgrade := charts.NewChartUpgrade(p.Name, p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, stackstateValues)
	chartUpgrades := []types.ChartUpgrade{*chartUpgrade}
	chartUpgradeAction := charts.NewChartUpgradeAction(p.Namespace, chartUpgrades)
	chartUpgradeAction.Timeout = &metav1.Duration{Duration: stackstateHelmActionTimeout}

	return chartUpgradeAction
}

// UninstallStackStateApp removes the app and its helm release from the
// cluster. A partial install leaves the release name taken and helm refuses
// every later install with "cannot re-use a name that is still in use", so
// re-entrant suites uninstall a stale app before installing again. Waits
// until the app object is gone.
func UninstallStackStateApp(client *rancher.Client, clusterID, namespace, appName string) error {
	uninstallAction := &types.ChartUninstallAction{
		Timeout: &metav1.Duration{Duration: stackstateHelmActionTimeout},
	}

	catalogClient, err := client.GetClusterCatalogClient(clusterID)
	if err != nil {
		return err
	}

	if err := retryChartAction(fmt.Sprintf("%s chart uninstall", appName), func() error {
		return catalogClient.UninstallChart(appName, namespace, uninstallAction)
	}); err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), chartTemplatePollInterval, chartTemplatePollTimeout, true, func(context.Context) (bool, error) {
		_, err := catalogClient.Apps(namespace).Get(context.TODO(), appName, metav1.GetOptions{})
		return k8sErrors.IsNotFound(err), nil
	})
}

// UpgradeStackStateServerChart upgrades the suse-observability server chart on the cluster
// carried by installOptions and waits for the app to transition to deployed state.
// additionalValues must carry the install-time server values (base config, ingress and HA
// sizing profile): the chart-upgrade path deliberately re-applies those values instead of
// a blind version bump, because upgrading on chart defaults would silently drop the HA
// sizing profile and the stackstate service credentials (url/api key/license), breaking
// the agent-to-server pipeline mid-upgrade (rancher/tests#946 Further Notes calls this
// per-product values migration out explicitly). Wrap calls in charts.RetryOnWatchError,
// as with UpgradeStackstateAgentChart, to absorb transient watch failures.
func UpgradeStackStateServerChart(client *rancher.Client, installOptions *charts.InstallOptions, additionalValues map[string]interface{}) error {
	serverSetting, err := client.Management.Setting.ByID(serverURLSettingID)
	if err != nil {
		return err
	}

	stackstateServerChartUpgradeActionPayload := &charts.PayloadOpts{
		InstallOptions: *installOptions,
		Name:           StackStateServerChartRepo,
		Namespace:      StackStateServerNamespace,
		Host:           serverSetting.Value,
	}

	chartUpgradeAction := newStackStateServerChartUpgradeAction(stackstateServerChartUpgradeActionPayload, additionalValues)

	catalogClient, err := client.GetClusterCatalogClient(installOptions.Cluster.ID)
	if err != nil {
		return err
	}

	err = retryChartAction("suse-observability server chart upgrade", func() error {
		return catalogClient.UpgradeChart(chartUpgradeAction, StackStateServerChartRepo)
	})
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
	watchAppInterface, err := adminCatalogClient.Apps(StackStateServerNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + StackStateServerChartRepo,
		TimeoutSeconds: &appWatchSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusFailed) {
			return false, fmt.Errorf("app %s entered failed state", app.Name)
		}
		if state == string(catalogv1.StatusPendingUpgrade) {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return err
	}

	watchAppInterface, err = adminCatalogClient.Apps(StackStateServerNamespace).Watch(context.TODO(), metav1.ListOptions{
		FieldSelector:  "metadata.name=" + StackStateServerChartRepo,
		TimeoutSeconds: &appWatchSeconds,
	})
	if err != nil {
		return err
	}

	err = wait.WatchWait(watchAppInterface, func(event watch.Event) (ready bool, err error) {
		app := event.Object.(*catalogv1.App)

		state := app.Status.Summary.State
		if state == string(catalogv1.StatusFailed) {
			return false, fmt.Errorf("app %s entered failed state", app.Name)
		}
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

// newStackStateServerChartUpgradeAction is a private helper function that returns the chart
// upgrade action carrying the install-time server values, mirroring the agent upgrade action.
func newStackStateServerChartUpgradeAction(p *charts.PayloadOpts, additionalValues map[string]interface{}) *types.ChartUpgradeAction {
	chartUpgrade := charts.NewChartUpgrade(p.Name, p.Name, p.Version, p.Cluster.ID, p.Cluster.Name, p.Host, p.DefaultRegistry, additionalValues)
	chartUpgrades := []types.ChartUpgrade{*chartUpgrade}
	chartUpgradeAction := charts.NewChartUpgradeAction(p.Namespace, chartUpgrades)
	chartUpgradeAction.Timeout = &metav1.Duration{Duration: stackstateHelmActionTimeout}

	return chartUpgradeAction
}

// CreateClusterRepo creates a new ClusterRepo resource in the Kubernetes cluster using the provided catalog client.
// It takes the client, repository name, and repository URL as arguments and returns an error if the operation fails.
// Creation is retried a bounded number of times with spacing: freshly deployed
// management clusters have answered ClusterRepo POSTs with bare internal errors
// while their catalog controllers and validators were still settling.
func CreateClusterRepo(client *rancher.Client, catalogClient *catalog.Client, name, url string) error {
	ctx := context.Background()
	repo := &rv1.ClusterRepo{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       rv1.RepoSpec{URL: url},
	}

	var lastErr error
	for attempt := 1; attempt <= clusterRepoCreateRetries; attempt++ {
		_, lastErr = catalogClient.ClusterRepos().Create(ctx, repo, metav1.CreateOptions{})
		if lastErr == nil {
			break
		}
		// A failed attempt may still have persisted server-side; a present
		// repo satisfies the caller's desired state, so treat AlreadyExists
		// as success and fall through to the cleanup registration.
		if k8sErrors.IsAlreadyExists(lastErr) {
			log.Infof("ClusterRepo %s already exists after a failed create attempt; treating as created", name)
			lastErr = nil
			break
		}
		log.Warnf("ClusterRepo %s create attempt %d/%d failed: %v", name, attempt, clusterRepoCreateRetries, lastErr)
		if attempt < clusterRepoCreateRetries {
			// Spacing between attempts: with immediate=false the first condition
			// check runs only after the interval, so this poll acts as the
			// context-aware sleep. Its error is ignored because the condition
			// succeeds at that first check (the timeout outlives the interval
			// and the context is never cancelled).
			_ = kwait.PollUntilContextTimeout(ctx, clusterRepoCreateRetryDelay, clusterRepoCreateRetryDelay+time.Second, false, func(context.Context) (bool, error) {
				return true, nil
			})
		}
	}
	if lastErr != nil {
		return fmt.Errorf("creating ClusterRepo %s at %s: %w", name, url, lastErr)
	}

	client.Session.RegisterCleanupFunc(func() error {
		propagation := metav1.DeletePropagationForeground
		err := catalogClient.ClusterRepos().Delete(context.Background(), name, metav1.DeleteOptions{PropagationPolicy: &propagation})
		if err != nil {
			return err
		}

		return err
	})
	return nil
}

// StructToMap Helper function to convert struct to map[string]interface{}
func StructToMap(obj interface{}) (map[string]interface{}, error) {
	data, err := yaml.Marshal(obj)
	if err != nil {
		return nil, err
	}

	var intermediate map[interface{}]interface{}
	if err = yaml.Unmarshal(data, &intermediate); err != nil {
		return nil, err
	}

	return convertMapInterfaceToMapString(intermediate), nil
}

// Converts map[interface{}]interface{} to map[string]interface{}
func convertMapInterfaceToMapString(obj map[interface{}]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range obj {
		key := fmt.Sprintf("%v", k)
		result[key] = convertToStringKeys(v)
	}
	return result
}

// convertToStringKeys converts any map[interface{}]interface{} to map[string]interface{} recursively
func convertToStringKeys(val interface{}) interface{} {
	switch v := val.(type) {
	case map[interface{}]interface{}:
		return convertMapToStringKeys(v)
	case []interface{}:
		convertedSlice := make([]interface{}, len(v))
		for i, v2 := range v {
			convertedSlice[i] = convertToStringKeys(v2)
		}
		return convertedSlice
	default:
		return v
	}
}

// convertMapToStringKeys converts a map[interface{}]interface{} to a map[string]interface{}
func convertMapToStringKeys(input map[interface{}]interface{}) map[string]interface{} {
	strMap := make(map[string]interface{})
	for k, v := range input {
		strKey := fmt.Sprintf("%v", k)
		strMap[strKey] = convertToStringKeys(v)
	}
	return strMap
}

// MergeValues merges multiple map[string]interface{} values into one, recursively merging nested maps if keys overlap.
func MergeValues(values ...map[string]interface{}) map[string]interface{} {
	result := make(map[string]interface{})
	for _, currentMap := range values {
		for key, currentValue := range currentMap {
			if existingValue, exists := result[key]; exists {
				// Check if both existing and current values are maps,
				// if so, recursively merge them
				mergedMap := mergeMaps(existingValue, currentValue)
				if mergedMap != nil {
					result[key] = mergedMap
					continue
				}
			}
			// Otherwise, overwrite with the current value
			result[key] = currentValue
		}
	}
	return result
}

// mergeMaps recursively merges two maps if both are maps, else returns nil
func mergeMaps(existingValue, currentValue interface{}) map[string]interface{} {
	existingMap, ok1 := existingValue.(map[string]interface{})
	currentMap, ok2 := currentValue.(map[string]interface{})
	if ok1 && ok2 {
		return MergeValues(existingMap, currentMap)
	}
	return nil
}

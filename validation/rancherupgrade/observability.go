//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/projects"
	interoperableCharts "github.com/rancher/tests/interoperability/charts"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/rancher/tests/interoperability/observability"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	rest "k8s.io/client-go/rest"
)

const (
	// localClusterName is the Rancher local cluster that always hosts the
	// suse-observability server chart, installed into its System project.
	localClusterName = "local"

	// systemProjectName is the built-in project hosting the deterministic
	// observability namespaces and chart installs on both clusters.
	systemProjectName = "System"

	// observabilityServerChartURL is the prime server-charts repository serving
	// the suse-observability chart (SUSE Observability is a prime-only product).
	observabilityServerChartURL = "https://charts.rancher.com/server-charts/prime/suse-observability"

	// stackstateConfigFileKey is the cattle-config key holding the StackState
	// connection settings (url, api key, license, admin password).
	stackstateConfigFileKey = "stackstateConfigs"

	// haSizingValuesPath points at the observability package's 150-ha sizing
	// profile. It is read from the sibling package instead of being forked into
	// this one so both suites stay pinned to a single source of truth for HA
	// sizing; `go test` always runs with the package directory as CWD.
	haSizingValuesPath = "../observability/resources/150-ha_sizing_values.yaml"

	// primeImageRegistry is the prime registry the suse-observability server
	// images pull from, matching validation/observability.
	primeImageRegistry = "registry.rancher.com"

	// catalogIndexPollInterval and catalogIndexPollTimeout bound the wait for
	// a freshly created ClusterRepo's chart index to be fetched and indexed
	// by the catalog controller.
	catalogIndexPollInterval = 10 * time.Second
	catalogIndexPollTimeout  = 5 * time.Minute

	// cattleSystemNamespace hosts the Rancher server pods on the local
	// cluster; rancherPodLabel selects them; rancherLogTailLines bounds the
	// server log capture on chart action failure, whose bare internal error
	// carries no client-visible cause.
	cattleSystemNamespace = "cattle-system"
	rancherPodLabel       = "app=rancher"
	rancherLogTailLines   = 300

	// ingressProxyBodySizeAnnotation mirrors the ingress annotation used by
	// validation/observability for the StackState server ingress.
	ingressProxyBodySizeAnnotation = "nginx.ingress.kubernetes.io/proxy-body-size"

	// ingressTLSSecretName mirrors the TLS secret name used by
	// validation/observability for the StackState server ingress.
	ingressTLSSecretName = "tls-secret"
)

// loadStackStateConfig returns the stackstateConfigs section of the test config.
func loadStackStateConfig() *observability.StackStateConfig {
	stackstateConfigs := new(observability.StackStateConfig)
	config.LoadConfig(stackstateConfigFileKey, stackstateConfigs)

	return stackstateConfigs
}

// loadHASizingValues reads the observability package's 150-ha sizing profile and
// returns it as suse-observability chart values, so this suite installs the same
// HA sizing profile as validation/observability without forking the values.
func loadHASizingValues() (map[string]interface{}, error) {
	sizingData, err := os.ReadFile(haSizingValuesPath)
	if err != nil {
		return nil, err
	}

	var sizingConfig observability.SizingConfigHA
	if err = yaml.Unmarshal(sizingData, &sizingConfig); err != nil {
		return nil, err
	}

	return interoperableCharts.StructToMap(sizingConfig)
}

// stackstateServerValues builds the suse-observability server chart values,
// mirroring validation/observability's HA-values installation: base config
// (prime registry, url, admin password, api key, license) plus ingress config,
// merged over the HA sizing profile.
func stackstateServerValues(stackstateConfigs *observability.StackStateConfig, sizingValues map[string]interface{}) (map[string]interface{}, error) {
	baseConfig := observability.BaseConfig{
		Global: observability.GlobalConfig{
			ImageRegistry: primeImageRegistry,
		},
		Stackstate: observability.StackstateServerConfig{
			BaseUrl:        stackstateConfigs.Url,
			Authentication: observability.AuthenticationConfig{AdminPassword: stackstateConfigs.AdminPassword},
			ApiKey:         observability.ApiKeyConfig{Key: stackstateConfigs.ClusterApiKey},
			License:        observability.LicenseConfig{Key: stackstateConfigs.License},
		},
	}

	ingressConfig := observability.IngressConfig{
		Ingress: observability.Ingress{
			Enabled: true,
			Annotations: map[string]string{
				ingressProxyBodySizeAnnotation: "50m",
			},
			Hosts: []observability.Host{
				{
					Host: stackstateConfigs.Url,
				},
			},
			TLS: []observability.TLSConfig{
				{
					Hosts:      []string{stackstateConfigs.Url},
					SecretName: ingressTLSSecretName,
				},
			},
		},
	}

	ingressValues, err := interoperableCharts.StructToMap(ingressConfig)
	if err != nil {
		return nil, err
	}

	baseValues, err := interoperableCharts.StructToMap(baseConfig)
	if err != nil {
		return nil, err
	}

	return interoperableCharts.MergeValues(ingressValues, baseValues, sizingValues), nil
}

// ensureNamespace is a no-op when namespaceName already exists on the cluster,
// otherwise it creates the namespace inside projectName. It is idempotent
// because the upgrade phases re-run as separate go test processes against
// namespaces the pre-upgrade phase already created.
func ensureNamespace(client *rancher.Client, clusterID, projectName, namespaceName string) error {
	existing, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
	if existing != nil {
		return nil
	}
	if !k8sErrors.IsNotFound(err) {
		return err
	}

	_, err = namespaces.CreateNamespace(client, clusterID, projectName, namespaceName, "", map[string]string{}, map[string]string{})

	return err
}

// systemProjectID returns the ID of the cluster's System project with the
// cluster prefix stripped, as required by the chart payload helpers.
func systemProjectID(client *rancher.Client, clusterID string) (string, error) {
	systemProject, err := projects.GetProjectByName(client, clusterID, systemProjectName)
	if err != nil {
		return "", err
	}
	if systemProject == nil || systemProject.ID == "" {
		return "", fmt.Errorf("system project not found on cluster %s", clusterID)
	}

	return strings.Split(systemProject.ID, ":")[1], nil
}

// waitForChartWorkloads waits until every deployment and daemonset in the
// namespace has its expected replica count available, the workload readiness
// gate used by the existing observability chart checks.
func waitForChartWorkloads(client *rancher.Client, clusterID, namespace string) error {
	if err := extencharts.WatchAndWaitDeployments(client, clusterID, namespace, meta.ListOptions{}); err != nil {
		return err
	}

	return extencharts.WatchAndWaitDaemonSets(client, clusterID, namespace, meta.ListOptions{})
}

// podStatusClusterName resolves the cluster identifier pods.StatusPods expects,
// mirroring validation/observability: provisioning clusters report the runtime
// cluster name via status, while clusters without a provisioning object (the
// local cluster) fall back to the ID lookup.
func podStatusClusterName(client *rancher.Client, displayName string) (string, error) {
	// The lookup error is ignored deliberately: a cluster without a
	// provisioning object (local) surfaces as a not-found style error, and the
	// fallback below resolves its ID; real API failures surface there too.
	clusterObject, _, _ := extensionscluster.GetProvisioningClusterByName(client, displayName, fleet.Namespace)
	if clusterObject != nil {
		status := new(provv1.ClusterStatus)
		if err := steveV1.ConvertToK8sType(clusterObject.Status, status); err != nil {
			return "", err
		}

		return status.ClusterName, nil
	}

	return extensionscluster.GetClusterIDByName(client, displayName)
}

// latestChartVersionAfterRepoCreate polls the cluster-scoped catalog for the
// latest chart version from a freshly created ClusterRepo. The catalog
// controller fetches and indexes the chart repo's index asynchronously, so the
// first version lookups after a create can fail before the repo is ready.
func latestChartVersionAfterRepoCreate(catalogClient *catalog.Client, chartName, repoName string) (string, error) {
	var latestVersion string
	err := kwait.PollUntilContextTimeout(context.TODO(), catalogIndexPollInterval, catalogIndexPollTimeout, true, func(context.Context) (bool, error) {
		version, err := catalogClient.GetLatestChartVersion(chartName, repoName)
		if err != nil {
			return false, nil
		}
		latestVersion = version
		return true, nil
	})
	if err != nil {
		return "", fmt.Errorf("chart %s from repo %s did not appear in the cluster-scoped catalog within %s: %w", chartName, repoName, catalogIndexPollTimeout, err)
	}
	return latestVersion, nil
}

// logRancherPodTail captures the tail of the Rancher server pod logs on the
// local cluster. Chart action failures surface as bare internal errors with no
// client-visible cause; the handler's failure reason lives in the server logs.
// This is best-effort diagnostic evidence: capture failures are logged and
// swallowed so they never mask the test's own failure.
func logRancherPodTail(client *rancher.Client, clusterID string) {
	restConfig := &rest.Config{
		Host:        "https://" + client.RancherConfig.Host + "/k8s/clusters/" + clusterID,
		BearerToken: client.RancherConfig.AdminToken,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: client.RancherConfig.Insecure != nil && *client.RancherConfig.Insecure,
			CAFile:   client.RancherConfig.CAFile,
			CAData:   []byte(client.RancherConfig.CACerts),
		},
	}
	k8s, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		log.Warnf("diagnostics: building the local cluster k8s client failed: %v", err)
		return
	}
	pods, err := k8s.CoreV1().Pods(cattleSystemNamespace).List(context.TODO(), meta.ListOptions{LabelSelector: rancherPodLabel})
	if err != nil {
		log.Warnf("diagnostics: listing pods with label %s in %s failed: %v", rancherPodLabel, cattleSystemNamespace, err)
		return
	}
	if len(pods.Items) == 0 {
		log.Warnf("diagnostics: no pods with label %s found in %s", rancherPodLabel, cattleSystemNamespace)
		return
	}
	tailLines := int64(rancherLogTailLines)
	serverLogs, err := k8s.CoreV1().Pods(cattleSystemNamespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{TailLines: &tailLines}).DoRaw(context.TODO())
	if err != nil {
		log.Warnf("diagnostics: fetching rancher logs from pod %s failed: %v", pods.Items[0].Name, err)
		return
	}
	log.Infof("diagnostics: rancher server log tail from pod %s:\n%s", pods.Items[0].Name, string(serverLogs))
}

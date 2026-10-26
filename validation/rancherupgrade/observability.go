//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/tests/actions/projects"
	interoperableCharts "github.com/rancher/tests/interoperability/charts"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/rancher/tests/interoperability/observability"
	log "github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
	corev1 "k8s.io/api/core/v1"
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

	// sizingValuesPath points at the observability package's 10-nonha
	// sizing profile. It is read from the sibling package instead of being
	// forked into this one so both suites stay pinned to a single source of
	// truth for sizing; `go test` always runs with the package directory as
	// CWD. The HA profile is not an option here: it requests ~27 cores,
	// which can never schedule on the pipeline's 3-node t3a.2xlarge cluster
	// (~22 allocatable cores), leaving pods Pending until the helm deadline.
	sizingValuesPath = "../observability/resources/10-nonha_sizing_values.yaml"

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

// loadSizingValues reads the observability package's 10-nonha sizing profile
// and returns it as suse-observability chart values. This suite validates
// continuity across a Rancher upgrade, not fleet-scale throughput, and runs
// against the pipeline's 3-node cluster, where the nonha profile is the only
// one whose resource requests fit.
func loadSizingValues() (map[string]interface{}, error) {
	sizingData, err := os.ReadFile(sizingValuesPath)
	if err != nil {
		return nil, err
	}

	var sizingConfig observability.SizingConfigNonHA
	if err = yaml.Unmarshal(sizingData, &sizingConfig); err != nil {
		return nil, err
	}

	return interoperableCharts.StructToMap(sizingConfig)
}

// stackstateServerValues builds the suse-observability server chart values:
// base config (prime registry, url, admin password, api key, license) plus
// ingress config, merged over the 10-nonha sizing profile.
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

	// An ingress host must be a bare DNS name: the configured URL doubles as
	// the agent's reporting endpoint (scheme and port included), so strip
	// those for the server chart's ingress host and TLS names.
	ingressHost := ingressHostFromURL(stackstateConfigs.Url)

	ingressConfig := observability.IngressConfig{
		Ingress: observability.Ingress{
			Enabled: true,
			Annotations: map[string]string{
				ingressProxyBodySizeAnnotation: "50m",
			},
			Hosts: []observability.Host{
				{
					Host: ingressHost,
				},
			},
			TLS: []observability.TLSConfig{
				{
					Hosts:      []string{ingressHost},
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

	mergedValues := interoperableCharts.MergeValues(ingressValues, baseValues, sizingValues)

	// Chart 2.12.0 removed stackstate.experimental (the server split moved to
	// per-component stackstate.components.*.split switches) and fails template
	// validation on the key; the shared HA sizing profile still carries it,
	// so prune it from the merged values.
	if stackstateValues, ok := mergedValues["stackstate"].(map[string]interface{}); ok {
		delete(stackstateValues, "experimental")
	}

	return mergedValues, nil
}

// ingressHostFromURL reduces a configured URL to its hostname for use as an
// ingress host, falling back to the raw value when it carries no scheme.
func ingressHostFromURL(configuredURL string) string {
	parsed, err := url.Parse(configuredURL)
	if err != nil || parsed.Hostname() == "" {
		return configuredURL
	}
	return parsed.Hostname()
}

// systemProjectID returns the ID of the cluster's System project in the
// cluster-prefixed form the chart actions require: the install action derives
// the target namespace's field.cattle.io/projectId annotation from this value
// (cluster:p-xxx). A bare p-xxx resolves to no project, the namespace's
// project roles never populate, and the action's thirty-second wait for them
// fails; builds 28 through 33 failed exactly there.
func systemProjectID(client *rancher.Client, clusterID string) (string, error) {
	systemProject, err := projects.GetProjectByName(client, clusterID, systemProjectName)
	if err != nil {
		return "", err
	}
	if systemProject == nil || systemProject.ID == "" {
		return "", fmt.Errorf("system project not found on cluster %s", clusterID)
	}

	return systemProject.ID, nil
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

// logInstallDiagnostics captures evidence when a chart action fails. The
// action may land on any Rancher server replica, so every replica's log tail
// is captured; the target namespace's conditions (InitialRolesPopulated) and
// the catalog Operation and App resources' statuses carry the helm
// operation's actual error. This is best-effort diagnostic evidence: capture
// failures are logged and swallowed so they never mask the test's own failure.
func logInstallDiagnostics(client *rancher.Client, catalogClient *catalog.Client, clusterID, namespace, appName string) {
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

	tailLines := int64(rancherLogTailLines)
	pods, err := k8s.CoreV1().Pods(cattleSystemNamespace).List(context.TODO(), meta.ListOptions{LabelSelector: rancherPodLabel})
	if err != nil {
		log.Warnf("diagnostics: listing pods with label %s in %s failed: %v", rancherPodLabel, cattleSystemNamespace, err)
	} else {
		for i := range pods.Items {
			serverLogs, err := k8s.CoreV1().Pods(cattleSystemNamespace).GetLogs(pods.Items[i].Name, &corev1.PodLogOptions{TailLines: &tailLines}).DoRaw(context.TODO())
			if err != nil {
				log.Warnf("diagnostics: fetching rancher logs from pod %s failed: %v", pods.Items[i].Name, err)
				continue
			}
			log.Infof("diagnostics: rancher server log tail from pod %s:\n%s", pods.Items[i].Name, string(serverLogs))
		}
	}

	if ns, err := k8s.CoreV1().Namespaces().Get(context.TODO(), namespace, meta.GetOptions{}); err != nil {
		log.Warnf("diagnostics: fetching namespace %s failed: %v", namespace, err)
	} else {
		log.Infof("diagnostics: namespace %s annotations=%v conditions=%v", namespace, ns.Annotations, ns.Status.Conditions)
	}

	if ops, err := catalogClient.Operations(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing catalog operations in %s failed: %v", namespace, err)
	} else {
		for i := range ops.Items {
			op := ops.Items[i]
			log.Infof("diagnostics: operation %s status=%+v", op.Name, op.Status)
			// The operation pod's helm container output names the chart-level
			// failure directly; capture it when the operation has a pod.
			if op.Status.PodName != "" {
				helmLogs, err := k8s.CoreV1().Pods(op.Status.PodNamespace).GetLogs(op.Status.PodName, &corev1.PodLogOptions{Container: "helm", TailLines: &tailLines}).DoRaw(context.TODO())
				if err != nil {
					log.Warnf("diagnostics: fetching helm logs from operation pod %s failed: %v", op.Status.PodName, err)
				} else {
					log.Infof("diagnostics: operation %s helm log tail:\n%s", op.Name, string(helmLogs))
				}
			}
		}
	}

	if app, err := catalogClient.Apps(namespace).Get(context.TODO(), appName, meta.GetOptions{}); err != nil {
		log.Warnf("diagnostics: fetching app %s in %s failed: %v", appName, namespace, err)
	} else {
		log.Infof("diagnostics: app %s status=%+v", appName, app.Status)
	}
}

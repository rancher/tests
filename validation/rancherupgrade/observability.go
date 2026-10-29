//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
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
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	utilyaml "k8s.io/apimachinery/pkg/util/yaml"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
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

	// primeImageRegistry is the prime registry the suse-observability server
	// images pull from, matching validation/observability.
	primeImageRegistry = "registry.rancher.com"

	// observabilitySizingProfile is the chart-native sizing profile for the
	// server install; 10-nonha fits the upgrade pipeline's three-node
	// cluster.
	observabilitySizingProfile = "10-nonha"

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
	// previousLogWindowLines, previousLogTailLines and previousLogErrorLimit
	// bound the crash-evidence capture from a restarted container's previous
	// instance: a wide window is filtered down to error-marker lines, with a
	// short raw tail kept for sequencing context.
	rancherLogTailLines    = 300
	previousLogWindowLines = 2000
	previousLogTailLines   = 15
	previousLogErrorLimit  = 40
	eventsDiagnosticsLimit = 40
)

const (
	// defaultStorageClassAnnotation marks a StorageClass as the cluster
	// default; the suse-observability PVCs request the default class.
	defaultStorageClassAnnotation = "storageclass.kubernetes.io/is-default-class"

	// localPathNamespace and localPathProvisionerName identify the
	// local-path-provisioner objects applied by ensureDefaultStorageClass.
	localPathNamespace       = "local-path-storage"
	localPathProvisionerName = "local-path-provisioner"

	// fieldManager is the server-side-apply field manager for manifests
	// applied by this suite.
	fieldManager = "rancher-tests-rancherupgrade"
)

// loadStackStateConfig returns the stackstateConfigs section of the test config.
func loadStackStateConfig() *observability.StackStateConfig {
	stackstateConfigs := new(observability.StackStateConfig)
	config.LoadConfig(stackstateConfigFileKey, stackstateConfigs)

	return stackstateConfigs
}

// stackstateServerValues builds the suse-observability server chart values
// in the chart's global configuration mode (global.suseObservability), the
// mode the 2.12.0 documentation installs with: license, baseUrl, admin
// password, receiver API key and the native 10-nonha sizing profile, which
// fits the pipeline's three-node cluster (about 7 cores and 27 GiB of
// requests). The legacy stackstate.* sizing path proved unusable there: its
// no-profile fallback sizes the split server components past their own
// memory limits, and a license installed through it never initializes the
// tenant subscription (build 39: every component exiting with
// TenantSubscriptionService$SubscriptionInitializationProhibited).
func stackstateServerValues(stackstateConfigs *observability.StackStateConfig) (map[string]interface{}, error) {
	return interoperableCharts.StructToMap(map[string]interface{}{
		"global": map[string]interface{}{
			"imageRegistry": primeImageRegistry,
			"suseObservability": map[string]interface{}{
				"license":        stackstateConfigs.License,
				"baseUrl":        stackstateConfigs.Url,
				"adminPassword":  stackstateConfigs.AdminPassword,
				"receiverApiKey": stackstateConfigs.ClusterApiKey,
				"sizing":         map[string]interface{}{"profile": observabilitySizingProfile},
			},
		},
	})
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

// previousStableChartVersion returns the newest stable chart version
// strictly below the repo's latest. The chart-upgrade phase needs a real
// delta: the prime repo's index is identical before and after the Rancher
// server upgrade, so seeding the pre-upgrade phase with the latest version
// leaves the phase nothing to upgrade to (build 43's guard fired exactly
// there). The version list arrives newest-first with -pre entries interleaved.
func previousStableChartVersion(catalogClient *catalog.Client, chartName, repoName string) (string, error) {
	versions, err := catalogClient.GetListChartVersions(chartName, repoName)
	if err != nil {
		return "", err
	}

	stable := make([]string, 0, len(versions))
	for _, version := range versions {
		if !strings.Contains(version, "-pre") {
			stable = append(stable, version)
		}
	}
	if len(stable) == 0 {
		return "", fmt.Errorf("chart %s from repo %s has no stable versions", chartName, repoName)
	}
	if len(stable) < 2 {
		return "", fmt.Errorf("chart %s from repo %s has no stable version below %s", chartName, repoName, stable[0])
	}

	return stable[1], nil
}

// logInstallDiagnostics captures evidence when a chart action fails. The
// action may land on any Rancher server replica, so every replica's log tail
// is captured; the target namespace's conditions (InitialRolesPopulated) and
// the catalog Operation and App resources' statuses carry the helm
// operation's actual error. This is best-effort diagnostic evidence: capture
// failures are logged and swallowed so they never mask the test's own failure.
func logInstallDiagnostics(client *rancher.Client, catalogClient *catalog.Client, clusterID, namespace, appName string) {
	k8s, err := clusterK8sClient(client, clusterID)
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
	logNamespaceWorkloadState(k8s, namespace)

	if classes, err := k8s.StorageV1().StorageClasses().List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing storage classes failed: %v", err)
	} else {
		for i := range classes.Items {
			log.Infof("diagnostics: storageclass %s default=%v provisioner=%s", classes.Items[i].Name, classes.Items[i].Annotations[defaultStorageClassAnnotation] == "true", classes.Items[i].Provisioner)
		}
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

// localPathProvisionerManifest is the upstream local-path-provisioner
// v0.0.30 deploy manifest (rancher/local-path-provisioner) with the default
// StorageClass annotation added; see the resource file for details.
//
//go:embed resources/local-path-provisioner.yaml
var localPathProvisionerManifest []byte

// clusterRestConfig builds the proxied REST config for the given cluster.
func clusterRestConfig(client *rancher.Client, clusterID string) *rest.Config {
	return &rest.Config{
		Host:        "https://" + client.RancherConfig.Host + "/k8s/clusters/" + clusterID,
		BearerToken: client.RancherConfig.AdminToken,
		TLSClientConfig: rest.TLSClientConfig{
			Insecure: client.RancherConfig.Insecure != nil && *client.RancherConfig.Insecure,
			CAFile:   client.RancherConfig.CAFile,
			CAData:   []byte(client.RancherConfig.CACerts),
		},
	}
}

// clusterK8sClient builds a Kubernetes client for the given cluster through
// Rancher's proxied API endpoint.
func clusterK8sClient(client *rancher.Client, clusterID string) (*kubernetes.Clientset, error) {
	return kubernetes.NewForConfig(clusterRestConfig(client, clusterID))
}

// ensureDefaultStorageClass guarantees the cluster has a default
// StorageClass. The upgrade pipeline's RKE2 clusters ship without any
// storage provisioner, so the suse-observability chart's PVCs, which request
// the cluster default class, stay Pending forever and helm --wait burns its
// full deadline (pipeline build 37's diagnostics: every namespace PVC
// Pending, every statefulset blocked on volume binding). When a default
// class already exists (Longhorn-enabled clusters) this is a no-op;
// otherwise the local-path-provisioner manifest is applied and its
// deployment waited on, mirroring how the longhorn module's suite provides
// its own storage.
func ensureDefaultStorageClass(client *rancher.Client, clusterID string) error {
	restConfig := clusterRestConfig(client, clusterID)
	k8s, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	classes, err := k8s.StorageV1().StorageClasses().List(context.TODO(), meta.ListOptions{})
	if err != nil {
		return err
	}
	for i := range classes.Items {
		if classes.Items[i].Annotations[defaultStorageClassAnnotation] == "true" {
			log.Infof("pre-upgrade phase: default storage class %s already present", classes.Items[i].Name)
			return nil
		}
	}

	log.Info("pre-upgrade phase: no default storage class; applying local-path-provisioner")
	if err := applyManifest(restConfig, localPathProvisionerManifest); err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 3*time.Minute, true, func(context.Context) (bool, error) {
		deployment, err := k8s.AppsV1().Deployments(localPathNamespace).Get(context.TODO(), localPathProvisionerName, meta.GetOptions{})
		if err != nil {
			return false, nil //nolint:staticcheck // transient lookup errors are retried by the poll
		}
		for i := range deployment.Status.Conditions {
			if deployment.Status.Conditions[i].Type == appsv1.DeploymentAvailable && deployment.Status.Conditions[i].Status == corev1.ConditionTrue {
				return true, nil
			}
		}
		return false, nil
	})
}

// applyManifest server-side applies every document of a multi-object YAML
// manifest to the cluster the REST config points at.
func applyManifest(restConfig *rest.Config, manifest []byte) error {
	dyn, err := dynamic.NewForConfig(restConfig)
	if err != nil {
		return err
	}

	discoveryClient, err := discovery.NewDiscoveryClientForConfig(restConfig)
	if err != nil {
		return err
	}
	groupResources, err := restmapper.GetAPIGroupResources(discoveryClient)
	if err != nil {
		return err
	}
	mapper := restmapper.NewDiscoveryRESTMapper(groupResources)

	decoder := utilyaml.NewYAMLOrJSONDecoder(bytes.NewReader(manifest), 4096)
	for {
		object := unstructured.Unstructured{}
		if err = decoder.Decode(&object); err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return err
		}
		if object.Object == nil {
			continue
		}

		mapping, err := mapper.RESTMapping(object.GroupVersionKind().GroupKind(), object.GroupVersionKind().Version)
		if err != nil {
			return fmt.Errorf("mapping %s: %w", object.GroupVersionKind(), err)
		}
		if _, err = dyn.Resource(mapping.Resource).Namespace(object.GetNamespace()).Apply(
			context.TODO(), object.GetName(), &object, meta.ApplyOptions{FieldManager: fieldManager, Force: true},
		); err != nil {
			return fmt.Errorf("applying %s %s: %w", object.GetKind(), object.GetName(), err)
		}
	}

	return nil
}

// logNamespaceWorkloadState dumps the readiness of every workload object in
// the chart namespace: pods with their container waiting reasons, restart
// counts, last termination (exit code, reason) and previous-container log
// tails, plus jobs, PVCs, deployments and statefulsets. helm --wait failing
// with "context deadline exceeded" names no culprit; this does. Best-effort
// like logInstallDiagnostics: capture failures are logged and swallowed.
func logNamespaceWorkloadState(k8s *kubernetes.Clientset, namespace string) {
	if pods, err := k8s.CoreV1().Pods(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing pods in %s failed: %v", namespace, err)
	} else {
		for i := range pods.Items {
			pod := pods.Items[i]
			waitStates := make([]string, 0, len(pod.Status.ContainerStatuses))
			crashed := make([]string, 0, len(pod.Status.ContainerStatuses))
			for _, cs := range pod.Status.ContainerStatuses {
				if cs.State.Waiting != nil {
					waitStates = append(waitStates, fmt.Sprintf("%s:%s(%s)", cs.Name, cs.State.Waiting.Reason, cs.State.Waiting.Message))
				}
				if cs.RestartCount > 0 {
					crashed = append(crashed, cs.Name)
					if t := cs.LastTerminationState.Terminated; t != nil {
						log.Infof("diagnostics: pod %s container %s last terminated: exitCode=%d reason=%s finished=%s", pod.Name, cs.Name, t.ExitCode, t.Reason, t.FinishedAt.String())
					}
					// The previous instance's log names the crash cause (OOM
					// stack trace, failed dependency, bad config); JVM boots
					// are verbose, so error-marker lines are surfaced from a
					// wide window alongside a short raw tail for context.
					tail := int64(previousLogWindowLines)
					previousLogs, logErr := k8s.CoreV1().Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: cs.Name, Previous: true, TailLines: &tail}).DoRaw(context.TODO())
					if logErr != nil {
						log.Warnf("diagnostics: fetching previous logs of %s/%s failed: %v", pod.Name, cs.Name, logErr)
					} else {
						lines := strings.Split(string(previousLogs), "\n")
						errorLines := make([]string, 0, previousLogErrorLimit)
						for _, line := range lines {
							if strings.Contains(line, "ERROR") || strings.Contains(line, "FATAL") || strings.Contains(line, "Exception") || strings.Contains(line, "Caused by") {
								errorLines = append(errorLines, line)
							}
						}
						if len(errorLines) > previousLogErrorLimit {
							errorLines = errorLines[len(errorLines)-previousLogErrorLimit:]
						}
						log.Infof("diagnostics: pod %s container %s previous-log errors:\n%s", pod.Name, cs.Name, strings.Join(errorLines, "\n"))
						raw := lines
						if len(raw) > previousLogTailLines {
							raw = raw[len(raw)-previousLogTailLines:]
						}
						log.Infof("diagnostics: pod %s container %s previous log tail:\n%s", pod.Name, cs.Name, strings.Join(raw, "\n"))
					}
				}
			}
			log.Infof("diagnostics: pod %s phase=%s restarts=%v waiting=%v crashedContainers=%v", pod.Name, pod.Status.Phase, containerRestarts(pod.Status.ContainerStatuses), waitStates, crashed)
		}
	}

	if jobs, err := k8s.BatchV1().Jobs(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing jobs in %s failed: %v", namespace, err)
	} else {
		for i := range jobs.Items {
			job := jobs.Items[i]
			log.Infof("diagnostics: job %s succeeded=%d failed=%d parallelism=%v conditions=%v", job.Name, job.Status.Succeeded, job.Status.Failed, job.Spec.Parallelism, job.Status.Conditions)
		}
	}

	if pvcs, err := k8s.CoreV1().PersistentVolumeClaims(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing PVCs in %s failed: %v", namespace, err)
	} else {
		for i := range pvcs.Items {
			pvc := pvcs.Items[i]
			log.Infof("diagnostics: pvc %s phase=%s", pvc.Name, pvc.Status.Phase)
		}
	}

	if deps, err := k8s.AppsV1().Deployments(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing deployments in %s failed: %v", namespace, err)
	} else {
		for i := range deps.Items {
			dep := deps.Items[i]
			log.Infof("diagnostics: deployment %s desired=%d ready=%d available=%d", dep.Name, *dep.Spec.Replicas, dep.Status.ReadyReplicas, dep.Status.AvailableReplicas)
		}
	}

	if sts, err := k8s.AppsV1().StatefulSets(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing statefulsets in %s failed: %v", namespace, err)
	} else {
		for i := range sts.Items {
			st := sts.Items[i]
			log.Infof("diagnostics: statefulset %s desired=%d ready=%d current=%d", st.Name, *st.Spec.Replicas, st.Status.ReadyReplicas, st.Status.CurrentReplicas)
		}
	}

	if events, err := k8s.CoreV1().Events(namespace).List(context.TODO(), meta.ListOptions{}); err != nil {
		log.Warnf("diagnostics: listing events in %s failed: %v", namespace, err)
	} else {
		// Volume-attach stalls and scheduling refusals appear only here:
		// the pod sits in ContainerCreating with no container-level evidence
		// (build 52's zookeeper stall would have been named by
		// FailedAttachVolume). Newest first, capped.
		sort.Slice(events.Items, func(i, j int) bool {
			return events.Items[i].LastTimestamp.After(events.Items[j].LastTimestamp.Time)
		})
		if len(events.Items) > eventsDiagnosticsLimit {
			events.Items = events.Items[:eventsDiagnosticsLimit]
		}
		for i := range events.Items {
			e := events.Items[i]
			log.Infof("diagnostics: event %s %s/%s %s: %s", e.LastTimestamp.String(), e.InvolvedObject.Kind, e.InvolvedObject.Name, e.Reason, e.Message)
		}
	}
}

// containerRestarts totals the restart counts of a pod's containers.
func containerRestarts(statuses []corev1.ContainerStatus) int32 {
	var restarts int32
	for i := range statuses {
		restarts += statuses[i].RestartCount
	}
	return restarts
}

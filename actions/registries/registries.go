package registries

import (
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
)

// CheckAllClusterPodsForRegistryPrefix checks every pod in a cluster and reports whether
// all container and init-container images that carry a registry FQDN start with the
// expected registry prefix.
//
// Note: this check is lenient by design. Images whose string carries no registry host
// (e.g. "rancher/foo", "nginx") resolve to Docker Hub and are skipped: on airgap
// clusters, RKE2/Rancher system pods (kube-system, calico-system, tigera-operator, ...)
// are mirrored at the containerd level, so their image strings keep the original
// registry (e.g. docker.io) even though they are pulled from the private registry.
// To assert that every image is prefixed, use CheckNamespacedPodsForRegistryPrefix.
func CheckAllClusterPodsForRegistryPrefix(client *rancher.Client, clusterID, registryPrefix string) (bool, error) {
	return checkPodsForRegistryPrefix(client, clusterID, "", registryPrefix, false)
}

// CheckNamespacedPodsForRegistryPrefix is the strict, namespace-scoped counterpart of
// CheckAllClusterPodsForRegistryPrefix: every container and init-container image of
// every pod in the namespace must start with registryPrefix — including images whose
// string carries no registry host ("rancher/foo", "nginx"), which would otherwise
// silently resolve to Docker Hub. Use it only for workloads whose images are all
// expected to be prefixed (e.g. charts rendered with global.cattle.systemDefaultRegistry,
// such as rancher-monitoring); workloads that legitimately rely on containerd mirrors
// for hostless images need the lenient cluster-wide check instead.
func CheckNamespacedPodsForRegistryPrefix(client *rancher.Client, clusterID, namespace, registryPrefix string) (bool, error) {
	return checkPodsForRegistryPrefix(client, clusterID, namespace, registryPrefix, true)
}

// checkPodsForRegistryPrefix lists pods (cluster-wide when namespace is empty, otherwise
// restricted to namespace) and reports false if any container or init-container image
// violates registryPrefix: host-qualified images that lack the prefix always fail,
// and in strict mode hostless images (Docker Hub references) fail too.
func checkPodsForRegistryPrefix(client *rancher.Client, clusterID, namespace, registryPrefix string, strict bool) (bool, error) {
	if strings.Contains(registryPrefix, "registry-1.docker.io") {
		logrus.Infof("Skipping registry prefix check for public docker registry: %s", registryPrefix)
		return true, nil
	}

	// Normalize to exactly one trailing slash so the comparison ends at a registry or
	// project boundary: prefix "registry.local/proxycache" must not match images under
	// "registry.local/proxycache-foreign/...". An empty prefix keeps the historical
	// no-op behavior (every image matches) that non-airgap callers rely on when
	// system-default-registry is unset.
	if registryPrefix != "" {
		registryPrefix = strings.TrimRight(registryPrefix, "/") + "/"
	}

	downstreamClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return false, err
	}

	steveClient := downstreamClient.SteveType(pods.PodResourceSteveType)
	var podsList *v1.SteveCollection
	if namespace == "" {
		podsList, err = steveClient.List(nil)
	} else {
		podsList, err = steveClient.NamespacedSteveClient(namespace).List(nil)
	}
	if err != nil {
		return false, err
	}

	if len(podsList.Data) == 0 {
		if namespace == "" {
			return false, fmt.Errorf("no pods found in cluster %s", clusterID)
		}
		return false, fmt.Errorf("no pods found in namespace %s of cluster %s", namespace, clusterID)
	}

	for _, pod := range podsList.Data {
		podSpec := &corev1.PodSpec{}
		err := v1.ConvertToK8sType(pod.Spec, podSpec)
		if err != nil {
			return false, err
		}

		images := make([]string, 0, len(podSpec.Containers)+len(podSpec.InitContainers))
		for _, container := range podSpec.Containers {
			images = append(images, container.Image)
		}
		for _, container := range podSpec.InitContainers {
			images = append(images, container.Image)
		}

		for _, image := range images {
			if !imageHasRegistryHost(image) && !strict {
				// Lenient mode: hostless images resolve to Docker Hub and are served by
				// containerd mirrors on airgap nodes, so their spec strings are not rewritten.
				logrus.Debugf("pod/containerImage %s/%s is using the public registry", pod.Name, image)
				continue
			}
			if !strings.HasPrefix(image, registryPrefix) {
				logrus.Warnf("pod/containerImage %s/%s is not using the correct registry prefix", pod.Name, image)
				return false, nil
			}
			logrus.Debugf("pod/containerImage %s/%s is using the expected registry prefix", pod.Name, image)
		}
	}
	return true, nil
}

// imageHasRegistryHost reports whether the image reference starts with an explicit
// registry host, following the Docker convention: the first path component counts as
// a host when it contains a "." or ":" (domain or port) or is "localhost". This is the
// same rule as hasRegistryHost in actions/monitoring/images.go.
func imageHasRegistryHost(image string) bool {
	first, _, found := strings.Cut(image, "/")
	if !found {
		return false
	}

	return first == "localhost" || strings.ContainsAny(first, ".:")
}

// CheckPodStatusImageSource is an extension that will check if the pod images are pulled from the
// correct registry and checks to see if pod status are in a ready nonerror state.
// Func will return a true if both checks are successful
func CheckPodStatusImageSource(client *rancher.Client, clusterName, registryFQDN string) (bool, []error) {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return false, []error{err}
	}

	podErrors := pods.StatusPods(client, clusterID)
	if len(podErrors) != 0 {
		return false, []error{fmt.Errorf("error: pod(s) are in an error state  %v", podErrors)}
	}

	correctRegistryFQDN, err := CheckAllClusterPodsForRegistryPrefix(client, clusterID, registryFQDN)
	if err != nil {
		return false, []error{fmt.Errorf("error: with checking cluster pod registry prefix: %v", err)}
	}

	if !correctRegistryFQDN {
		return false, []error{fmt.Errorf("error: pod images were not pulled from the correct registry")}
	}

	return true, nil
}

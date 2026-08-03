package storage

import (
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	client "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/kubeapi/volumes/persistentvolumeclaims"
	namespaceActions "github.com/rancher/tests/actions/namespaces"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

var (
	PersistentVolumeEntityType = "persistentvolume"
)

// CheckNodeFilesystem Runs a command in a pod that has the specified node's filesystem mounted in /host.
// We do this in a separate namespace to ease cleanup.
// If the command fails the test mediated by the provided T object will fail.
func CheckNodeFilesystem(t *testing.T, client *rancher.Client, clusterID string, nodeName string, command string, project *client.Project) {
	// Create a new namespace and a debug pod within it to check the host filesystem for the custom Longhorn data directory.
	// We do this in a separate namespace to ease cleanup.
	debugNamespace := generateResourceName("debug", clusterID, nodeName)

	t.Logf("Create namespace [%v] to check node filesystem with debugger pod", debugNamespace)
	createdNamespace, err := namespaceActions.CreateNamespace(client, debugNamespace, "{}", nil, nil, project)
	require.NoError(t, err)

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	client.Session.RegisterCleanupFunc(func() error {
		return steveClient.SteveType(namespaceActions.NamespaceSteveType).Delete(createdNamespace)
	})

	checkDataPathCommand := []string{"kubectl", "debug", "node/" + nodeName, "-n", debugNamespace, "--profile=general", "--image=busybox", "--", "/bin/sh", "-c", command}
	_, err = kubectl.Command(client, nil, clusterID, checkDataPathCommand, "")
	require.NoError(t, err)

	// This polling strategy was chosen because some operations done through this command can take a long time while most should take very little.
	// The total wait time amounts to roughly 34 minutes.
	backoff := kwait.Backoff{
		Duration: 1 * time.Second,
		Factor:   1.4,
		Jitter:   0,
		Steps:    20,
	}

	err = kwait.ExponentialBackoff(backoff, func() (bool, error) {
		debugPods, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(debugNamespace).List(nil)
		require.NotEmpty(t, debugPods)
		require.NoError(t, err)

		podStatus := &corev1.PodStatus{}
		err = steveV1.ConvertToK8sType(debugPods.Data[0].Status, podStatus)
		require.NoError(t, err)

		switch podStatus.Phase {
		case "Failed":
			return true, fmt.Errorf("Failed running command on node %s", nodeName)
		case "Succeeded":
			return true, nil
		default:
			return false, nil
		}
	})

	require.NoError(t, err)
}

// CheckMountedVolume Checks writes to a specific path inside the specified pod and checks if it succeeded.
// The goal of this function is to test whether mounted volumes are working as expected.
func CheckMountedVolume(t *testing.T, kubeConfig *clientcmd.ClientConfig, clusterID string, namespace string, podName string, mountpoint string) {
	var restConfig *rest.Config
	restConfig, err := (*kubeConfig).ClientConfig()
	require.NoError(t, err)

	testFileName := generateResourceName("test-file", clusterID, podName)

	t.Logf("Write to mounted volume under the path [%v] on pod [%v]", mountpoint+"/"+testFileName, podName)
	writeToMountedVolume := []string{"touch", mountpoint + "/" + testFileName}
	output, err := kubeconfig.KubectlExec(restConfig, podName, namespace, writeToMountedVolume)
	if err != nil {
		t.Logf("Command failed on pod %s: %s", podName, output)
	}
	require.NoError(t, err)

	checkFileOnVolume := []string{"stat", mountpoint + "/" + testFileName}
	output, err = kubeconfig.KubectlExec(restConfig, podName, namespace, checkFileOnVolume)
	if err != nil {
		t.Logf("Command failed on pod %s: %s", podName, output)
	}
	require.NoError(t, err)
}

// CheckVolumeAllocation checks if every pod in a namespace has an attached volume according to some expected parameters.
func CheckVolumeAllocation(t *testing.T, client *rancher.Client, clusterID string, namespace string, storageClass string, volumeSourceName string, mountpoint string) {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	pods, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(namespace).List(nil)
	require.NoError(t, err)
	t.Logf("Check all %d pods have a healthy %s volume mounted on %s", len(pods.Data), storageClass, mountpoint)

	pvcs, err := steveClient.SteveType(persistentvolumeclaims.PersistentVolumeClaimType).NamespacedSteveClient(namespace).List(nil)
	require.NoError(t, err)

	kubeConfig, err := kubeconfig.GetKubeconfig(client, clusterID)
	require.NoError(t, err)

	for _, pod := range pods.Data {
		targetVolume, err := GetTargetVolume(pod, volumeSourceName)
		require.NoError(t, err)

		var pvcSpec corev1.PersistentVolumeClaimSpec
		for _, pvc := range pvcs.Data {
			if pvc.Name == targetVolume.PersistentVolumeClaim.ClaimName {
				err = steveV1.ConvertToK8sType(pvc.Spec, &pvcSpec)
				require.NoError(t, err)
				break
			}
		}
		require.Equal(t, storageClass, *pvcSpec.StorageClassName)

		CheckMountedVolume(t, kubeConfig, clusterID, namespace, pod.Name, mountpoint)
	}
}

// generateResourceName generates a unique resource name using the provided parts while avoiding that the name is longer than 63 characters.
func generateResourceName(prefix string, parts ...string) string {
	basename := prefix
	for _, v := range parts {
		basename += "-" + v
	}

	// The full name must be at most 63 characters long and AppendRandomString adds 11 characters.
	if len(basename) > 52 {
		hash := sha256.New()
		hash.Write([]byte(basename))
		basename = prefix + "-" + fmt.Sprintf("%x", hash.Sum(nil))
		basename = basename[0:52]
	}

	return namegenerator.AppendRandomString(basename)
}

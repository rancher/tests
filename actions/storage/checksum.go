package storage

import (
	"context"
	"crypto/sha256"
	"fmt"
	"regexp"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/tests/actions/kubeapi/volumes/persistentvolumeclaims"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/rest"
)

// ChecksumDataFile is the filename written under MountPath by CreateChecksumWorkload.
const ChecksumDataFile = "checksum-data"

// checksumContentPattern constrains the content accepted by CreateChecksumWorkload:
// content is interpolated into a single-quoted sh -c command, so a value outside
// alphanumeric/hyphen (notably a quote) would break the quoting. Enforced in code
// so a future caller cannot silently inject shell.
var checksumContentPattern = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// CreateChecksumWorkload creates a 1GiB PVC named <name> on storageClassName in the
// default namespace, mounts it into a single-replica nginx deployment named <name> at
// MountPath, waits for rollout, then writes content to MountPath/ChecksumDataFile via
// kubectl exec. Deterministic names let a later test phase (running as a separate
// process) relocate the resources. content must be shell-safe (alphanumeric/hyphen);
// it is interpolated into an sh -c command. No session cleanup is registered: callers
// are upgrade tests whose resources must outlive the process.
func CreateChecksumWorkload(client *rancher.Client, clusterID string, storageClassName string, name string, content string) error {
	if !checksumContentPattern.MatchString(content) {
		return fmt.Errorf("content %q must match %s: it is interpolated into a single-quoted sh -c command",
			content, checksumContentPattern.String())
	}

	storageClass, err := GetStorageClass(client, clusterID, storageClassName)
	if err != nil {
		return err
	}

	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	accessModes := []corev1.PersistentVolumeAccessMode{
		"ReadWriteOnce",
	}

	// Deliberate deviation from CreatePVC: no PV-delete/Longhorn-volume cleanup funcs
	// are registered, because these resources must survive into later upgrade phases.
	_, err = persistentvolumeclaims.CreatePersistentVolumeClaim(
		client,
		clusterID,
		name,
		"test-pvc-volume",
		namespaces.Default,
		1,
		accessModes,
		nil,
		&storageClass,
	)
	if err != nil {
		return err
	}

	pvc := &corev1.PersistentVolumeClaim{}
	// TenMinuteTimeout: the first volume on a freshly installed Longhorn waits
	// on engine-image pull, instance-manager startup, and CSI registration,
	// which routinely exceeds a minute on a cold cluster.
	err = wait.PollUntilContextTimeout(context.Background(), pollInterval, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		pvc, err = wrangler.Core.PersistentVolumeClaim().Get(namespaces.Default, name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		return pvc.Status.Phase == persistentvolumeclaims.PersistentVolumeBoundStatus, nil
	})
	if err != nil {
		return err
	}
	logrus.Debugf("PVC %s is Bound to volume %s", pvc.Name, pvc.Spec.VolumeName)

	// Shared nginx+PVC deployment construction with CreatePVCWorkload; the
	// deterministic name makes PVC, volume mount, deployment, and relocation
	// label all equal name, so later phases re-find the workload from a
	// separate process.
	_, err = createNginxDeploymentWithPVC(client, clusterID, name, name, name)
	if err != nil {
		return err
	}

	pod, err := getChecksumPod(client, clusterID, name)
	if err != nil {
		return err
	}

	restConfig, err := getDownstreamRestConfig(client, clusterID)
	if err != nil {
		return err
	}

	writeCommand := []string{"sh", "-c", "printf '%s' '" + content + "' > " + MountPath + "/" + ChecksumDataFile}
	logrus.Debugf("Writing %s/%s on pod %s", MountPath, ChecksumDataFile, pod.Name)
	_, err = kubeconfig.KubectlExec(restConfig, pod.Name, namespaces.Default, writeCommand)

	return err
}

// VerifyChecksumWorkload relocates the PVC and pod created by CreateChecksumWorkload,
// asserts the PVC is still Bound, and asserts the sha256 of MountPath/ChecksumDataFile
// inside the running pod equals sha256(content).
func VerifyChecksumWorkload(client *rancher.Client, clusterID string, name string, content string) error {
	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	pvc, err := wrangler.Core.PersistentVolumeClaim().Get(namespaces.Default, name, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if pvc.Status.Phase != persistentvolumeclaims.PersistentVolumeBoundStatus {
		return fmt.Errorf("PVC %s is %s, expected %s", name, pvc.Status.Phase, persistentvolumeclaims.PersistentVolumeBoundStatus)
	}

	pod, err := getChecksumPod(client, clusterID, name)
	if err != nil {
		return err
	}

	restConfig, err := getDownstreamRestConfig(client, clusterID)
	if err != nil {
		return err
	}

	checksumCommand := []string{"sha256sum", MountPath + "/" + ChecksumDataFile}
	output, err := kubeconfig.KubectlExec(restConfig, pod.Name, namespaces.Default, checksumCommand)
	if err != nil {
		return err
	}

	// The exec runs with a TTY, so the output carries \r line endings; strip them
	// before extracting the hash (first whitespace-separated field).
	outputFields := strings.Fields(strings.ReplaceAll(output.String(), "\r", ""))
	if len(outputFields) == 0 {
		return fmt.Errorf("empty sha256sum output on pod %s", pod.Name)
	}

	actualChecksum := outputFields[0]
	expectedChecksum := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch on pod %s: expected %s, got %s", pod.Name, expectedChecksum, actualChecksum)
	}

	return nil
}

// getChecksumPod returns the pod in the default namespace labeled with
// DeploymentIdentifierLabel == name, waiting until it exists and is Running.
func getChecksumPod(client *rancher.Client, clusterID string, name string) (*steveV1.SteveAPIObject, error) {
	var checksumPod *steveV1.SteveAPIObject
	err := wait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		steveclient, err := client.Steve.ProxyDownstream(clusterID)
		if err != nil {
			return false, err
		}

		podsResp, err := steveclient.SteveType(stevetypes.Pod).NamespacedSteveClient(namespaces.Default).List(nil)
		if err != nil {
			return false, err
		}

		for i := range podsResp.Data {
			pod := &podsResp.Data[i]
			if pod.Labels[DeploymentIdentifierLabel] != name {
				continue
			}

			podStatus := &corev1.PodStatus{}
			err = steveV1.ConvertToK8sType(pod.Status, podStatus)
			if err != nil {
				return false, err
			}

			if podStatus.Phase != corev1.PodRunning {
				logrus.Debugf("Pod %s is %s, waiting for Running", pod.Name, podStatus.Phase)
				return false, nil
			}

			checksumPod = pod
			return true, nil
		}

		return false, nil
	})

	return checksumPod, err
}

// getDownstreamRestConfig builds a rest config for kubectl exec against the cluster.
func getDownstreamRestConfig(client *rancher.Client, clusterID string) (*rest.Config, error) {
	kubeConfig, err := kubeconfig.GetKubeconfig(client, clusterID)
	if err != nil {
		return nil, err
	}

	return (*kubeConfig).ClientConfig()
}

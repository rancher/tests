package storage

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rancher/rancher/pkg/api/scheme"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	wloads "github.com/rancher/shepherd/extensions/workloads"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/kubeapi/storageclasses"
	"github.com/rancher/tests/actions/kubeapi/volumes/persistentvolumeclaims"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/wait"
)

const (
	nginxName                     = "nginx"
	MountPath                     = "/auto-mnt"
	defaultStorageClassAnnotation = "storageclass.kubernetes.io/is-default-class"
	pollInterval                  = time.Duration(1 * time.Second)
)

// GetStorageClass gets a storage class with the provided name on the provided cluster.
// If an empty storageClassName is provided, the first one on the list of available storage classes will be used.
func GetStorageClass(client *rancher.Client, clusterID string, storageClassName string) (storagev1.StorageClass, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return storagev1.StorageClass{}, err
	}

	storageClassVolumesResource := dynamicClient.Resource(storageclasses.StorageClassGroupVersionResource).Namespace("")
	unstructuredResp, err := storageClassVolumesResource.List(context.Background(), metav1.ListOptions{})
	if err != nil {
		return storagev1.StorageClass{}, err
	}

	storageClasses := &storagev1.StorageClassList{}
	err = scheme.Scheme.Convert(unstructuredResp, storageClasses, unstructuredResp.GroupVersionKind())
	if err != nil {
		return storagev1.StorageClass{}, err
	}

	if len(storageClasses.Items) == 0 {
		return storagev1.StorageClass{}, fmt.Errorf("No storage classes available on cluster %s with user %s", clusterID, client.UserID)
	}

	if storageClassName == "" {
		for _, storageClass := range storageClasses.Items {
			if storageClass.Annotations[defaultStorageClassAnnotation] == "true" {
				return storageClass, nil
			}
		}

		return storageClasses.Items[0], nil
	}

	var storageClass storagev1.StorageClass
	found := false
	for _, class := range storageClasses.Items {
		if class.Name == storageClassName {
			found = true
			storageClass = class
			break
		}
	}

	if !found {
		return storageClass, fmt.Errorf("No storage class named %s", storageClassName)
	}

	return storageClass, nil
}

// CreatePVCWorkload creates a workload with a PVC for storage using the provided storageClassName.
// This helper should be used to test storage class functionality, i.e. for an in-tree / out-of-tree cloud provider.
// If an empty storageClassName is provided, the first one on the list will be used.
func CreatePVCWorkload(t *testing.T, client *rancher.Client, clusterID string, storageClassName string) *steveV1.SteveAPIObject {
	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	steveclient, err := adminClient.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	storageClass, err := GetStorageClass(client, clusterID, storageClassName)
	require.NoError(t, err)

	logrus.Infof("creating PVC")

	accessModes := []corev1.PersistentVolumeAccessMode{
		"ReadWriteOnce",
	}

	persistentVolumeClaim, err := persistentvolumeclaims.CreatePersistentVolumeClaim(
		client,
		clusterID,
		namegenerator.AppendRandomString("pvc"),
		"test-pvc-volume",
		namespaces.Default,
		1,
		accessModes,
		nil,
		&storageClass,
	)
	require.NoError(t, err)

	pvcStatus := &corev1.PersistentVolumeClaimStatus{}
	stevePvc := &steveV1.SteveAPIObject{}

	err = wait.PollUntilContextTimeout(t.Context(), pollInterval, defaults.OneMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		stevePvc, err = steveclient.SteveType(persistentvolumeclaims.PersistentVolumeClaimType).ByID(namespaces.Default + "/" + persistentVolumeClaim.Name)
		require.NoError(t, err)

		err = steveV1.ConvertToK8sType(stevePvc.Status, pvcStatus)
		require.NoError(t, err)

		if pvcStatus.Phase == persistentvolumeclaims.PersistentVolumeBoundStatus {
			return true, nil
		}
		return false, err
	})
	require.NoError(t, err)

	nginxResponse, err := createNginxDeploymentWithPVC(steveclient, "pvcwkld", persistentVolumeClaim.Name, string(stevePvc.Spec.(map[string]interface{})[persistentvolumeclaims.StevePersistentVolumeClaimVolumeName].(string)))
	require.NoError(t, err)

	return nginxResponse
}

// createNginxDeploymentWithPVC is a helper function that creates a nginx deployment in a cluster's default namespace
func createNginxDeploymentWithPVC(steveclient *steveV1.Client, containerNamePrefix, pvcName, volName string) (*steveV1.SteveAPIObject, error) {
	logrus.Tracef("Vol: %s", volName)
	logrus.Tracef("Pod: %s", pvcName)

	containerName := namegenerator.AppendRandomString(containerNamePrefix)
	volMount := &corev1.VolumeMount{
		MountPath: MountPath,
		Name:      volName,
	}

	podVol := corev1.Volume{
		Name: volName,
		VolumeSource: corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: pvcName,
			},
		},
	}

	containerTemplate := wloads.NewContainer(nginxName, nginxName, corev1.PullAlways, []corev1.VolumeMount{*volMount}, []corev1.EnvFromSource{}, nil, nil, nil)
	podTemplate := wloads.NewPodTemplate([]corev1.Container{containerTemplate}, []corev1.Volume{podVol}, []corev1.LocalObjectReference{}, nil, nil)
	deployment := wloads.NewDeploymentTemplate(containerName, namespaces.Default, podTemplate, true, nil)

	deploymentResp, err := steveclient.SteveType(stevetypes.Deployment).Create(deployment)
	if err != nil {
		return nil, err
	}

	return deploymentResp, err
}

// CreateAndDeleteVolume creates a volume using the provided storage class and then deletes it.
// This function has the purpose of helping to test RBAC on operations related to storage.
func CreateAndDeleteVolume(client *rancher.Client, clusterID string, namespace string, storageClass storagev1.StorageClass) error {
	persistentVolumeClaim, err := persistentvolumeclaims.CreatePersistentVolumeClaim(
		client,
		clusterID,
		namegenerator.AppendRandomString("pvc"),
		"test-rbac-project-user",
		namespace,
		1,
		[]corev1.PersistentVolumeAccessMode{
			"ReadWriteOnce",
		},
		nil,
		&storageClass,
	)
	if err != nil {
		return err
	}

	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return err
	}

	PersistentVolumeClaimResource := dynamicClient.Resource(persistentvolumeclaims.PersistentVolumeClaimGroupVersionResource).Namespace(namespace)

	return PersistentVolumeClaimResource.Delete(context.Background(), persistentVolumeClaim.Name, metav1.DeleteOptions{})
}

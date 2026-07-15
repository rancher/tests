package statefulsets

import (
	"github.com/rancher/shepherd/clients/rancher"
	extstatefulsetapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/statefulsets"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/storage"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateStatefulset is a helper to create a statefulset using wrangler context.
// If storageClass is provided, a volume template with the indicated storage class and 1Gi of storage will be included in the StetefulSet spec.
func CreateStatefulSet(client *rancher.Client, clusterID, namespaceName string, podTemplate corev1.PodTemplateSpec, replicas int32, waitForReady bool, storageClassName string) (*appv1.StatefulSet, error) {
	statefulsetTemplate := NewStatefulsetTemplate(namespaceName, podTemplate, replicas)
	if storageClassName != "" {
		volName := namegen.AppendRandomString(storageClassName + "-pvc-template")
		volumeClaimTemplate := corev1.PersistentVolumeClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      volName,
				Namespace: namespaceName,
			},
			Spec: corev1.PersistentVolumeClaimSpec{
				AccessModes: []corev1.PersistentVolumeAccessMode{
					"ReadWriteOnce",
				},
				StorageClassName: &storageClassName,
				Resources: corev1.VolumeResourceRequirements{
					Requests: corev1.ResourceList{
						corev1.ResourceStorage: resource.MustParse("1Gi"),
					},
				},
			},
		}

		statefulsetTemplate.Spec.VolumeClaimTemplates = append(statefulsetTemplate.Spec.VolumeClaimTemplates, volumeClaimTemplate)
		for i, container := range statefulsetTemplate.Spec.Template.Spec.Containers {
			container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
				MountPath: storage.MountPath,
				Name:      volumeClaimTemplate.Name,
				ReadOnly:  false,
			})
			statefulsetTemplate.Spec.Template.Spec.Containers[i] = container
		}
	}

	createdStatefulset, err := extstatefulsetapi.CreateStatefulSetWithTemplate(client, clusterID, statefulsetTemplate, waitForReady)
	if err != nil {
		return nil, err
	}

	if waitForReady {
		err = extstatefulsetapi.WaitForStatefulSetReady(client, clusterID, createdStatefulset.Namespace, createdStatefulset.Name)
		if err != nil {
			return nil, err
		}
	}

	return createdStatefulset, err
}

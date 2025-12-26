package statefulset

import (
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/rancher/tests/actions/storage"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateStatefulset is a helper to create a statefulset using wrangler context.
// If storageClass is provided, a volume template with the indicated storage class and 5Gi of storage will
// be included in the StetefulSet spec.
func CreateStatefulSet(client *rancher.Client, clusterID, namespaceName string, podTemplate corev1.PodTemplateSpec, replicas int32, watchStatefulset bool, storageClassName string) (*appv1.StatefulSet, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	statefulsetTemplate := NewStatefulsetTemplate(namespaceName, podTemplate, replicas)
	if storageClassName != "" {
		volName := namegenerator.AppendRandomString(storageClassName + "-pvc-template")
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
						corev1.ResourceStorage: resource.MustParse("5Gi"),
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

	createdStatefulset, err := wranglerContext.Apps.StatefulSet().Create(statefulsetTemplate)
	if err != nil {
		return nil, err
	}

	if watchStatefulset {
		err = charts.WatchAndWaitStatefulSets(client, clusterID, namespaceName, metav1.ListOptions{
			FieldSelector: "metadata.name=" + createdStatefulset.Name,
		})
		if err != nil {
			return nil, err
		}
	}

	return createdStatefulset, err
}

// CreateStatefulSetFromConfig creates a Pod from a config using steve
func CreateStatefulSetFromConfig(client *v1.Client, clusterID string, statefulSet *appv1.StatefulSet) (*appv1.StatefulSet, error) {
	statefulSetResp, err := client.SteveType("apps.statefulset").Create(statefulSet)
	if err != nil {
		return nil, err
	}

	newStatefulSet := new(appv1.StatefulSet)
	err = v1.ConvertToK8sType(statefulSetResp.JSONResp, newStatefulSet)
	if err != nil {
		return nil, err
	}

	return newStatefulSet, nil
}

// UpdateStatefulSet is a helper to update statefulset using wrangler context
func UpdateStatefulSet(client *rancher.Client, clusterID, namespaceName string, statefulset *appv1.StatefulSet, watchStatefulset bool) (*appv1.StatefulSet, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	latestStatefulset, err := wranglerContext.Apps.StatefulSet().Get(namespaceName, statefulset.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	statefulset.ResourceVersion = latestStatefulset.ResourceVersion

	updatedStatefulset, err := wranglerContext.Apps.StatefulSet().Update(statefulset)
	if err != nil {
		return nil, err
	}

	if watchStatefulset {
		err = charts.WatchAndWaitStatefulSets(client, clusterID, namespaceName, metav1.ListOptions{
			FieldSelector: "metadata.name=" + updatedStatefulset.Name,
		})
		if err != nil {
			return nil, err
		}
	}

	return updatedStatefulset, nil
}

// DeleteStatefulSet is a helper to delete a statefulset using wrangler context
func DeleteStatefulSet(client *rancher.Client, clusterID string, statefulset *appv1.StatefulSet) error {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = wranglerContext.Apps.StatefulSet().Delete(statefulset.Namespace, statefulset.Name, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return nil
}

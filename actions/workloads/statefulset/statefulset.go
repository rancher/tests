package statefulset

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/charts"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateStatefulset is a helper to create a statefulset using wrangler context
func CreateStatefulSet(client *rancher.Client, clusterID, namespaceName string, podTemplate corev1.PodTemplateSpec, replicas int32, watchStatefulset bool) (*appv1.StatefulSet, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	statefulsetTemplate := NewStatefulsetTemplate(namespaceName, podTemplate, replicas)
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

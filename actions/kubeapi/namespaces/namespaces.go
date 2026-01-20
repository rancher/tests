package namespaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/api/scheme"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ContainerDefaultResourceLimitAnnotation = "field.cattle.io/containerDefaultResourceLimit"
	ProjectIDAnnotation                     = "field.cattle.io/projectId"
	ResourceQuotaAnnotation                 = "field.cattle.io/resourceQuota"
	ResourceQuotaStatusAnnotation           = "cattle.io/status"
	InitialUsedResourceQuotaValue           = "0"
)

// NamespaceGroupVersionResource is the required Group Version Resource for accessing namespaces in a cluster,
// using the dynamic client.
var NamespaceGroupVersionResource = schema.GroupVersionResource{
	Group:    "",
	Version:  "v1",
	Resource: "namespaces",
}

// ContainerDefaultResourceLimit sets the container default resource limit in a string
// limitsCPU and requestsCPU in form of "3m"
// limitsMemory and requestsMemory in the form of "3Mi"
func ContainerDefaultResourceLimit(limitsCPU, limitsMemory, requestsCPU, requestsMemory string) string {
	containerDefaultResourceLimit := fmt.Sprintf("{\"limitsCpu\": \"%s\", \"limitsMemory\":\"%s\",\"requestsCpu\":\"%s\",\"requestsMemory\":\"%s\"}",
		limitsCPU, limitsMemory, requestsCPU, requestsMemory)
	return containerDefaultResourceLimit
}

// GetNamespaceByName is a helper function that returns the namespace by name in a specific cluster, uses ListNamespaces to get the namespace.
func GetNamespaceByName(client *rancher.Client, clusterID, namespaceName string) (*corev1.Namespace, error) {
	namespace := new(corev1.Namespace)

	dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
	if err != nil {
		return nil, err
	}

	namespaceResource := dynamicClient.Resource(NamespaceGroupVersionResource).Namespace("")
	unstructuredNamespace, err := namespaceResource.Get(context.TODO(), namespaceName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	if err = scheme.Scheme.Convert(unstructuredNamespace, namespace, unstructuredNamespace.GroupVersionKind()); err != nil {
		return nil, err
	}

	return namespace, nil
}

// WaitForProjectIDUpdate is a helper that waits for the project-id annotation and label to be updated in a specified namespace
func WaitForProjectIDUpdate(client *rancher.Client, clusterID, projectName, namespaceName string) error {
	expectedAnnotations := map[string]string{
		ProjectIDAnnotation: clusterID + ":" + projectName,
	}

	expectedLabels := map[string]string{
		ProjectIDAnnotation: projectName,
	}

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {

		namespace, pollErr := GetNamespaceByName(client, clusterID, namespaceName)
		if pollErr != nil {
			return false, pollErr
		}

		for key, expectedValue := range expectedAnnotations {
			if actualValue, ok := namespace.Annotations[key]; !ok || actualValue != expectedValue {
				return false, nil
			}
		}

		for key, expectedValue := range expectedLabels {
			if actualValue, ok := namespace.Labels[key]; !ok || actualValue != expectedValue {
				return false, nil
			}
		}

		return true, nil
	})

	if err != nil {
		return err
	}

	return nil
}

// UpdateNamespaceResourceQuotaAnnotation updates the resource quota annotation on a namespace
func UpdateNamespaceResourceQuotaAnnotation(client *rancher.Client, clusterID string, namespaceName string, existingLimits map[string]string, extendedLimits map[string]string) error {
	wranglerCtx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	limit := make(map[string]interface{}, len(existingLimits)+1)
	for k, v := range existingLimits {
		limit[k] = v
	}
	if len(extendedLimits) > 0 {
		limit["extended"] = extendedLimits
	}

	quota := map[string]interface{}{"limit": limit}
	quotaJSON, err := json.Marshal(quota)
	if err != nil {
		return fmt.Errorf("marshal resource quota annotation: %w", err)
	}

	quotaStr := string(quotaJSON)

	ns, err := wranglerCtx.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if ns.Annotations == nil {
		ns.Annotations = make(map[string]string)
	}

	ns.Annotations[ResourceQuotaAnnotation] = quotaStr

	_, err = UpdateNamespace(client, clusterID, ns)
	if err != nil {
		return fmt.Errorf("failed to update namespace %s with resource quota annotation: %w", namespaceName, err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		updatedNS, err := wranglerCtx.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if updatedNS.Annotations[ResourceQuotaAnnotation] == quotaStr {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return fmt.Errorf("failed to verify namespace %s annotation update: %w", namespaceName, err)
	}

	return nil
}

// MoveNamespaceToProject updates the project annotation/label to move the namespace into a different project
func MoveNamespaceToProject(client *rancher.Client, clusterID, namespaceName, newProjectName string) error {
	ns, err := GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return fmt.Errorf("failed to get namespace %s: %w", namespaceName, err)
	}

	if ns.Annotations == nil {
		ns.Annotations = make(map[string]string)
	}
	if ns.Labels == nil {
		ns.Labels = make(map[string]string)
	}

	ns.Annotations[ProjectIDAnnotation] = fmt.Sprintf("%s:%s", clusterID, newProjectName)
	ns.Labels[ProjectIDAnnotation] = newProjectName

	clusterContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return fmt.Errorf("failed to get wrangler context for cluster %s: %w", clusterID, err)
	}
	latestNS, err := GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return fmt.Errorf("failed to fetch namespace %s: %w", namespaceName, err)
	}
	ns.ResourceVersion = latestNS.ResourceVersion

	if _, err := clusterContext.Core.Namespace().Update(ns); err != nil {
		return fmt.Errorf("failed to update namespace %s with new project annotation: %w", namespaceName, err)
	}

	if err := WaitForProjectIDUpdate(client, clusterID, newProjectName, namespaceName); err != nil {
		return fmt.Errorf("project ID annotation/label not updated for namespace %s: %w", namespaceName, err)
	}

	return nil
}

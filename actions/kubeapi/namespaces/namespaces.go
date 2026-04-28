package namespaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ContainerDefaultResourceLimitAnnotation = "field.cattle.io/containerDefaultResourceLimit"
	ProjectIDAnnotation                     = "field.cattle.io/projectId"
	ResourceQuotaAnnotation                 = "field.cattle.io/resourceQuota"
	ResourceQuotaStatusAnnotation           = "cattle.io/status"
	InitialUsedResourceQuotaValue           = "0"
)

// ContainerDefaultResourceLimit sets the container default resource limit in a string
// limitsCPU and requestsCPU in form of "3m"
// limitsMemory and requestsMemory in the form of "3Mi"
func ContainerDefaultResourceLimit(limitsCPU, limitsMemory, requestsCPU, requestsMemory string) string {
	containerDefaultResourceLimit := fmt.Sprintf("{\"limitsCpu\": \"%s\", \"limitsMemory\":\"%s\",\"requestsCpu\":\"%s\",\"requestsMemory\":\"%s\"}",
		limitsCPU, limitsMemory, requestsCPU, requestsMemory)
	return containerDefaultResourceLimit
}

// CreateNamespace creates a namespace using wrangler context in a project in a specific cluster and waits for project ID propagation.
func CreateNamespace(client *rancher.Client, clusterID, projectName, namespaceName, containerDefaultResourceLimit string, labels, annotations map[string]string) (*corev1.Namespace, error) {
	if annotations == nil {
		annotations = make(map[string]string)
	}

	if containerDefaultResourceLimit != "" {
		annotations[ContainerDefaultResourceLimitAnnotation] = containerDefaultResourceLimit
	}

	if projectName != "" {
		annotationValue := clusterID + ":" + projectName
		annotations[ProjectIDAnnotation] = annotationValue
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name:        namespaceName,
			Annotations: annotations,
			Labels:      labels,
		},
	}

	createdNamespace, err := extnamespaceapi.CreateNamespace(client, clusterID, namespace)
	if err != nil {
		return nil, err
	}

	if projectName != "" {
		err = WaitForProjectIDUpdate(client, clusterID, projectName, namespaceName)
		if err != nil {
			return nil, err
		}
	}

	return createdNamespace, nil
}

// CreateMultipleNamespacesInProject creates multiple namespaces in the specified project using wrangler context
func CreateMultipleNamespacesInProject(client *rancher.Client, clusterID, projectID string, count int) ([]*corev1.Namespace, error) {
	var createdNamespaces []*corev1.Namespace

	for i := 0; i < count; i++ {
		ns, err := CreateNamespace(client, clusterID, projectID, namegen.AppendRandomString("testns"), "", nil, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to create namespace %d/%d: %w", i+1, count, err)
		}

		createdNamespaces = append(createdNamespaces, ns)
	}

	return createdNamespaces, nil
}

// GetNamespacesInProject retrieves all namespaces in a specific project within a cluster
func GetNamespacesInProject(client *rancher.Client, clusterID, projectName string) ([]*corev1.Namespace, error) {
	nsList, err := extnamespaceapi.ListNamespaces(client, clusterID, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("%s=%s", ProjectIDAnnotation, projectName),
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list namespaces for project %s: %w", projectName, err)
	}

	namespaces := make([]*corev1.Namespace, 0, len(nsList.Items))
	for i := range nsList.Items {
		namespaces = append(namespaces, &nsList.Items[i])
	}

	return namespaces, nil
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

		namespace, pollErr := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
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

	ns, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return err
	}

	if ns.Annotations == nil {
		ns.Annotations = make(map[string]string)
	}

	ns.Annotations[ResourceQuotaAnnotation] = quotaStr

	_, err = extnamespaceapi.UpdateNamespace(client, clusterID, ns)
	if err != nil {
		return fmt.Errorf("failed to update namespace %s with resource quota annotation: %w", namespaceName, err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		updatedNS, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
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
	ns, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
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

	if _, err := extnamespaceapi.UpdateNamespace(client, clusterID, ns); err != nil {
		return fmt.Errorf("failed to update namespace %s with new project annotation: %w", namespaceName, err)
	}

	if err := WaitForProjectIDUpdate(client, clusterID, newProjectName, namespaceName); err != nil {
		return fmt.Errorf("project ID annotation/label not updated for namespace %s: %w", namespaceName, err)
	}

	return nil
}

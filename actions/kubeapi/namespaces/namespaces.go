package namespaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
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

// GetNamespaceAnnotation is a helper to retrieve and parse a namespace annotation value as a map
func GetNamespaceAnnotation(client *rancher.Client, clusterID string, namespaceName, annotationKey string) (map[string]interface{}, error) {
	namespace, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return nil, err
	}

	if namespace.Annotations == nil {
		return nil, fmt.Errorf("namespace %q has no annotations", namespaceName)
	}

	nsAnnotation, exists := namespace.Annotations[annotationKey]
	if !exists || nsAnnotation == "" {
		return nil, fmt.Errorf("annotation %q not found on namespace %q", annotationKey, namespaceName)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(nsAnnotation), &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal annotation %q: %w", annotationKey, err)
	}

	return data, nil
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

// ContainerDefaultResourceLimit sets the container default resource limit in a string
// limitsCPU and requestsCPU in form of "3m"
// limitsMemory and requestsMemory in the form of "3Mi"
func ContainerDefaultResourceLimit(limitsCPU, limitsMemory, requestsCPU, requestsMemory string) string {
	containerDefaultResourceLimit := fmt.Sprintf("{\"limitsCpu\": \"%s\", \"limitsMemory\":\"%s\",\"requestsCpu\":\"%s\",\"requestsMemory\":\"%s\"}",
		limitsCPU, limitsMemory, requestsCPU, requestsMemory)
	return containerDefaultResourceLimit
}

// GetConditionStatusAndMessageFromAnnotation is a helper to parse the annotation value for a specific condition type and return the status and message.
func GetConditionStatusAndMessageFromAnnotation(annotation string, conditionType string) (string, string, error) {
	var annotationData map[string][]map[string]string
	if err := json.Unmarshal([]byte(annotation), &annotationData); err != nil {
		return "", "", fmt.Errorf("error parsing JSON: %v", err)
	}

	conditions, ok := annotationData["Conditions"]
	if !ok {
		return "", "", fmt.Errorf("no 'Conditions' found in annotation")
	}

	for _, condition := range conditions {
		if condition["Type"] == conditionType {
			status := condition["Status"]
			message := condition["Message"]

			return status, message, nil
		}
	}

	return "", "", fmt.Errorf("no condition of type '%s' found", conditionType)
}

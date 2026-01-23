package namespaces

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	quotas "github.com/rancher/tests/actions/kubeapi/resourcequotas"
	"github.com/rancher/tests/actions/workloads/pods"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyAnnotationInNamespace checks if a specific annotation exists or not in a namespace.
func VerifyAnnotationInNamespace(client *rancher.Client, clusterID, namespaceName, annotationKey string, expectedToExist bool) error {
	namespace, err := GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return err
	}

	exists := false
	if namespace.Annotations != nil {
		_, exists = namespace.Annotations[annotationKey]
	}

	if expectedToExist && !exists {
		return fmt.Errorf("expected annotation %q to exist, but it does not", annotationKey)
	}

	if !expectedToExist && exists {
		return fmt.Errorf("expected annotation %q to not exist, but it does", annotationKey)
	}

	return nil
}

// VerifyNamespaceResourceQuota verifies that the namespace resource quota contains the expected hard limits.
func VerifyNamespaceResourceQuota(client *rancher.Client, clusterID, namespaceName string, expectedQuota map[string]string) error {
	resourceQuotas, err := quotas.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(resourceQuotas.Items) != 1 {
		return fmt.Errorf("expected 1 ResourceQuota, got %d", len(resourceQuotas.Items))
	}

	actualHard := resourceQuotas.Items[0].Spec.Hard

	for resourceName, expectedValue := range expectedQuota {
		actualQuantity, exists := actualHard[corev1.ResourceName(resourceName)]
		if !exists {
			return fmt.Errorf("expected resource %q not found in ResourceQuota", resourceName)
		}

		expectedQuantity := resource.MustParse(expectedValue)

		if actualQuantity.Cmp(expectedQuantity) != 0 {
			return fmt.Errorf("resource %q mismatch: expected=%s actual=%s", resourceName, expectedQuantity.String(), actualQuantity.String())
		}
	}

	return nil
}

// VerifyLimitRange verifies that the LimitRange in the specified namespace matches the expected CPU and memory limits and requests.
func VerifyLimitRange(client *rancher.Client, clusterID, namespaceName string, expectedCPULimit, expectedCPURequest, expectedMemoryLimit, expectedMemoryRequest string) error {
	clusterContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	var limitRanges []corev1.LimitRange
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TenSecondTimeout, true, func(ctx context.Context) (bool, error) {
		limitRangeList, err := clusterContext.Core.LimitRange().List(namespaceName, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		if len(limitRangeList.Items) == 0 {
			return false, nil
		}
		limitRanges = limitRangeList.Items
		return true, nil
	})

	if err != nil {
		return fmt.Errorf("limit range not found in namespace %s after waiting: %v", namespaceName, err)
	}

	if len(limitRanges) != 1 {
		return fmt.Errorf("expected limit range count is 1, but got %d", len(limitRanges))
	}

	limitRange := limitRanges[0].Spec
	if len(limitRange.Limits) == 0 {
		return fmt.Errorf("no limits found in limit range spec")
	}

	limits := limitRange.Limits[0]

	if actualCPULimit, ok := limits.Default[corev1.ResourceCPU]; !ok || actualCPULimit.String() != expectedCPULimit {
		return fmt.Errorf("cpu limit mismatch: expected %s, got %s", expectedCPULimit, actualCPULimit.String())
	}

	if actualCPURequest, ok := limits.DefaultRequest[corev1.ResourceCPU]; !ok || actualCPURequest.String() != expectedCPURequest {
		return fmt.Errorf("cpu request mismatch: expected %s, got %s", expectedCPURequest, actualCPURequest.String())
	}

	if actualMemoryLimit, ok := limits.Default[corev1.ResourceMemory]; !ok || actualMemoryLimit.String() != expectedMemoryLimit {
		return fmt.Errorf("memory limit mismatch: expected %s, got %s", expectedMemoryLimit, actualMemoryLimit.String())
	}

	if actualMemoryRequest, ok := limits.DefaultRequest[corev1.ResourceMemory]; !ok || actualMemoryRequest.String() != expectedMemoryRequest {
		return fmt.Errorf("memory request mismatch: expected %s, got %s", expectedMemoryRequest, actualMemoryRequest.String())
	}

	return nil
}

// VerifyContainerResources checks if the container resources in a pod created by a deployment match the expected values.
func VerifyContainerResources(client *rancher.Client, clusterID, namespaceName, deploymentName, cpuLimit, cpuRequest, memoryLimit, memoryRequest string) error {
	var errs []string

	podNames, err := pods.GetPodNamesFromDeployment(client, clusterID, namespaceName, deploymentName)
	if err != nil {
		return fmt.Errorf("error fetching pod by deployment name: %w", err)
	}

	if len(podNames) == 0 {
		return fmt.Errorf("expected at least one pod, got 0")
	}

	pod, err := pods.GetPodByName(client, clusterID, namespaceName, podNames[0])
	if err != nil {
		return err
	}

	if len(pod.Spec.Containers) == 0 {
		return fmt.Errorf("no containers found in pod %q", pod.Name)
	}

	normalizeString := func(s string) string {
		if s == "" {
			return "0"
		}
		return s
	}

	cpuLimit = normalizeString(cpuLimit)
	cpuRequest = normalizeString(cpuRequest)
	memoryLimit = normalizeString(memoryLimit)
	memoryRequest = normalizeString(memoryRequest)

	containerResources := pod.Spec.Containers[0].Resources
	containerCPULimit := containerResources.Limits[corev1.ResourceCPU]
	containerCPURequest := containerResources.Requests[corev1.ResourceCPU]
	containerMemoryLimit := containerResources.Limits[corev1.ResourceMemory]
	containerMemoryRequest := containerResources.Requests[corev1.ResourceMemory]

	if cpuLimit != containerCPULimit.String() {
		errs = append(errs, fmt.Sprintf("CPU limit mismatch: expected=%s actual=%s", cpuLimit, containerCPULimit.String()))

	}
	if cpuRequest != containerCPURequest.String() {
		errs = append(errs, fmt.Sprintf("CPU request mismatch: expected=%s actual=%s", cpuRequest, containerCPURequest.String()))
	}
	if memoryLimit != containerMemoryLimit.String() {
		errs = append(errs, fmt.Sprintf("Memory limit mismatch: expected=%s actual=%s", memoryLimit, containerMemoryLimit.String()))
	}
	if memoryRequest != containerMemoryRequest.String() {
		errs = append(errs, fmt.Sprintf("Memory request mismatch: expected=%s actual=%s", memoryRequest, containerMemoryRequest.String()))
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "\n"))
	}

	return nil
}

// VerifyUsedNamespaceResourceQuota checks if the used resources in a namespace's ResourceQuota match the expected values.
func VerifyUsedNamespaceResourceQuota(client *rancher.Client, clusterID, namespace string, expectedUsed map[string]string) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	rqList, err := ctx.Core.ResourceQuota().List(namespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(rqList.Items) != 1 {
		return fmt.Errorf("expected exactly 1 ResourceQuota in namespace %s, found %d", namespace, len(rqList.Items))
	}

	rq := rqList.Items[0]

	for resource, expected := range expectedUsed {
		actualQty, ok := rq.Status.Used[corev1.ResourceName(resource)]
		if !ok {
			return fmt.Errorf("resource %q not found in ResourceQuota.Status.Used", resource)
		}

		if actualQty.String() != expected {
			return fmt.Errorf("resource quota used mismatch for %q: expected=%s actual=%s", resource, expected, actualQty.String())
		}
	}

	return nil
}

// VerifyNamespaceResourceQuotaValidationStatus checks if the resource quota annotation in a namespace matches the expected limits and validation status.
func VerifyNamespaceResourceQuotaValidationStatus(client *rancher.Client, clusterID, namespaceName string,
	expectedExistingLimits map[string]string,
	expectedExtendedLimits map[string]string,
	expectedStatus bool,
	expectedErrorMessage string,
) error {

	namespace, err := GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return err
	}

	annotationData, err := getNamespaceAnnotationAsMap(client, clusterID, namespace.Name, ResourceQuotaAnnotation)
	if err != nil {
		return err
	}

	limitMap, ok := annotationData["limit"].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid quota annotation format: missing 'limit'")
	}

	for resource, expectedValue := range expectedExistingLimits {
		actual, ok := limitMap[resource]
		if !ok {
			return fmt.Errorf("existing resource %q not found in namespace quota annotation", resource)
		}

		actualStr := fmt.Sprintf("%v", actual)
		if actualStr != expectedValue {
			return fmt.Errorf("existing quota mismatch for %q: expected=%s actual=%s", resource, expectedValue, actualStr)
		}
	}

	if len(expectedExtendedLimits) > 0 {
		extendedMap, ok := limitMap["extended"].(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid quota annotation format: missing 'limit.extended'")
		}

		for resource, expectedValue := range expectedExtendedLimits {
			actual, ok := extendedMap[resource]
			if !ok {
				return fmt.Errorf(
					"extended resource %q not found in namespace quota annotation",
					resource,
				)
			}

			actualStr := fmt.Sprintf("%v", actual)
			if actualStr != expectedValue {
				return fmt.Errorf("extended quota mismatch for %q: expected=%s actual=%s", resource, expectedValue, actualStr)
			}
		}
	}

	statusAnnotation, ok := namespace.Annotations[ResourceQuotaStatusAnnotation]
	if !ok {
		return fmt.Errorf("missing %q annotation on namespace", ResourceQuotaStatusAnnotation)
	}

	status, message, err := getConditionStatusAndMessageFromAnnotation(statusAnnotation, "ResourceQuotaValidated")
	if err != nil {
		return err
	}

	if (status == "True") != expectedStatus {
		return fmt.Errorf("resource quota validation status mismatch: expected=%t actual=%s", expectedStatus, status)
	}

	if expectedErrorMessage != "" && !strings.Contains(message, expectedErrorMessage) {
		return fmt.Errorf("expected error message to contain %q, got %q", expectedErrorMessage, message)
	}

	return nil
}

func getNamespaceAnnotationAsMap(client *rancher.Client, clusterID string, namespaceName, annotationKey string) (map[string]interface{}, error) {
	namespace, err := GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return nil, err
	}

	if namespace.Annotations == nil {
		return nil, fmt.Errorf("namespace %q has no annotations", namespaceName)
	}

	limitAnnotation, exists := namespace.Annotations[annotationKey]
	if !exists || limitAnnotation == "" {
		return nil, fmt.Errorf("annotation %q not found on namespace %q", annotationKey, namespaceName)
	}

	var data map[string]interface{}
	if err := json.Unmarshal([]byte(limitAnnotation), &data); err != nil {
		return nil, fmt.Errorf("failed to unmarshal annotation %q: %w", annotationKey, err)
	}

	return data, nil
}

func getConditionStatusAndMessageFromAnnotation(annotation string, conditionType string) (string, string, error) {
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

// VerifyNamespaceHasNoResourceQuota checks that there are no ResourceQuota objects in the specified namespace.
func VerifyNamespaceHasNoResourceQuota(client *rancher.Client, clusterID, namespace string) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	rqList, err := ctx.Core.ResourceQuota().List(namespace, metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(rqList.Items) != 0 {
		return fmt.Errorf("expected no ResourceQuota in namespace %s, but found %d", namespace, len(rqList.Items))
	}

	return nil
}

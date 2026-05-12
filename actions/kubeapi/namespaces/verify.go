package namespaces

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	quotaapi "github.com/rancher/tests/actions/kubeapi/resourcequotas"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyNamespaceResourceQuota verifies that the namespace resource quota contains the expected hard limits.
func VerifyNamespaceResourceQuota(client *rancher.Client, clusterID, namespaceName string, expectedQuota map[string]string) error {
	resourceQuotas, err := quotaapi.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{})
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

// VerifyUsedNamespaceResourceQuota checks if the used resources in a namespace's ResourceQuota match the expected values.
func VerifyUsedNamespaceResourceQuota(client *rancher.Client, clusterID, namespaceName string, expectedUsed map[string]string) error {
	resourceQuotas, err := quotaapi.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(resourceQuotas.Items) != 1 {
		return fmt.Errorf("expected exactly 1 ResourceQuota in namespace %s, found %d", namespaceName, len(resourceQuotas.Items))
	}

	rq := resourceQuotas.Items[0]

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

// VerifyNamespaceHasNoResourceQuota checks that there are no ResourceQuota objects in the specified namespace.
func VerifyNamespaceHasNoResourceQuota(client *rancher.Client, clusterID, namespaceName string) error {
	rqList, err := quotaapi.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{})
	if err != nil {
		return err
	}

	if len(rqList.Items) != 0 {
		return fmt.Errorf("expected no ResourceQuota in namespace %s, but found %d", namespaceName, len(rqList.Items))
	}

	return nil
}

// VerifyNamespaceResourceQuotaValidationStatus checks if the resource quota annotation in a namespace matches the expected limits and validation status, with polling.
func VerifyNamespaceResourceQuotaValidationStatus(client *rancher.Client, clusterID, namespaceName string,
	expectedExistingLimits, expectedExtendedLimits map[string]string,
	expectedStatus bool,
	expectedErrorMessage string,
) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		annotationData, err := GetNamespaceAnnotation(client, clusterID, namespaceName, ResourceQuotaAnnotation)
		if err != nil {
			return false, nil
		}

		limitMap, ok := annotationData["limit"].(map[string]interface{})
		if !ok {
			return false, nil
		}

		for resource, expectedValue := range expectedExistingLimits {
			actual, ok := limitMap[resource]
			if !ok {
				return false, nil
			}

			actualStr := fmt.Sprintf("%v", actual)
			if actualStr != expectedValue {
				return false, nil
			}
		}

		if len(expectedExtendedLimits) > 0 {
			extendedMap, ok := limitMap["extended"].(map[string]interface{})
			if !ok {
				return false, nil
			}

			for resource, expectedValue := range expectedExtendedLimits {
				actual, ok := extendedMap[resource]
				if !ok {
					return false, nil
				}

				actualStr := fmt.Sprintf("%v", actual)
				if actualStr != expectedValue {
					return false, nil
				}
			}
		}

		namespace, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
		if err != nil {
			return false, nil
		}
		statusAnnotation, ok := namespace.Annotations[ResourceQuotaStatusAnnotation]
		if !ok {
			return false, nil
		}

		status, message, err := GetConditionStatusAndMessageFromAnnotation(statusAnnotation, "ResourceQuotaValidated")
		if err != nil {
			return false, nil
		}

		if (status == "True") != expectedStatus {
			return false, nil
		}

		if expectedErrorMessage != "" && !strings.Contains(message, expectedErrorMessage) {
			return false, nil
		}

		return true, nil
	})
}

// VerifyNamespacePodResourceQuota checks if the namespace resource quota contains the expected hard limits for pods.
func VerifyNamespacePodResourceQuota(client *rancher.Client, clusterID, namespaceName string, expectedPodLimit int) error {
	quotas, err := quotaapi.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{})
	if err != nil {
		return err
	}
	if len(quotas.Items) != 1 {
		return fmt.Errorf("expected resource quota count is 1, but got %d", len(quotas.Items))
	}

	resourceList := quotas.Items[0].Spec.Hard
	actualPodLimit, ok := resourceList[corev1.ResourcePods]
	if !ok {
		return fmt.Errorf("pod limit not found in the resource quota")
	}
	podLimit := int(actualPodLimit.Value())
	if podLimit != expectedPodLimit {
		return fmt.Errorf("pod limit in the resource quota: %d does not match the expected value: %d", podLimit, expectedPodLimit)
	}

	return nil
}

// VerifyNamespacePodQuotaValidationStatus checks if the resource quota annotation in a namespace matches the expected pod limits and validation status.
func VerifyNamespacePodQuotaValidationStatus(client *rancher.Client, clusterID, namespaceName, namespacePodLimit string, expectedStatus bool, expectedErrorMessage string) error {
	namespace, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
	if err != nil {
		return err
	}

	limitData, err := GetNamespaceAnnotation(client, clusterID, namespace.Name, ResourceQuotaAnnotation)
	if err != nil {
		return err
	}
	actualNamespacePodLimit := limitData["limit"].(map[string]interface{})["pods"]

	if actualNamespacePodLimit != namespacePodLimit {
		return fmt.Errorf("namespace pod limit mismatch in the namespace spec. expected: %s, actual: %s", namespacePodLimit, actualNamespacePodLimit)
	}

	status, message, err := GetConditionStatusAndMessageFromAnnotation(namespace.Annotations[ResourceQuotaStatusAnnotation], "ResourceQuotaValidated")
	if err != nil {
		return err
	}

	if (status == "True") != expectedStatus {
		return fmt.Errorf("resource quota validation status mismatch. expected: %t, actual: %s", expectedStatus, status)
	}

	if !strings.Contains(message, expectedErrorMessage) {
		return fmt.Errorf("Error message does not contain expected substring: %s", expectedErrorMessage)
	}

	return nil
}

// VerifyLimitRange verifies that the LimitRange in the specified namespace matches the expected CPU and memory limits and requests.
func VerifyLimitRange(client *rancher.Client, clusterID, namespaceName string, expectedCPULimit, expectedCPURequest, expectedMemoryLimit, expectedMemoryRequest string) error {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	var limitRanges []corev1.LimitRange
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
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

// VerifyNoLimitRange checks that there are no LimitRange objects in the specified namespace.
func VerifyNoLimitRange(client *rancher.Client, clusterID, namespaceName string) error {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		limitRangeList, err := clusterContext.Core.LimitRange().List(namespaceName, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		if len(limitRangeList.Items) == 0 {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		return fmt.Errorf("limit range still present in namespace %s: %v", namespaceName, err)
	}
	return nil
}

// VerifyAnnotationExistsInNamespace checks if the specified annotation key exists in the namespace and matches the expected existence, with polling.
func VerifyAnnotationExistsInNamespace(client *rancher.Client, clusterID string, namespaceName string, annotationKey string, shouldExist bool) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		namespace, err := extnamespaceapi.GetNamespaceByName(client, clusterID, namespaceName)
		if err != nil {
			return false, err
		}
		_, exists := namespace.Annotations[annotationKey]
		if exists == shouldExist {
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		errorMessage := fmt.Sprintf("Annotation '%s' should%s exist", annotationKey, map[bool]string{true: "", false: " not"}[shouldExist])
		return errors.New(errorMessage)
	}

	return nil
}

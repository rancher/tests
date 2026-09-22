package namespaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extquotaapi "github.com/rancher/shepherd/extensions/kubeapi/resourcequotas"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ContainerDefaultResourceLimitAnnotation = "field.cattle.io/containerDefaultResourceLimit"
	ProjectIDAnnotation                     = "field.cattle.io/projectId"
	ResourceQuotaAnnotation                 = "field.cattle.io/resourceQuota"
	ResourceQuotaStatusAnnotation           = "cattle.io/status"
	InitialUsedResourceQuotaValue           = "0"
	ManagedResourceQuotaLabel               = "resourcequota.management.cattle.io/default-resource-quota"
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

// NewResourceQuota builds a ResourceQuota with the given hard limits, optionally carrying the Rancher-managed marker label.
func NewResourceQuota(namespaceName, name, hardCPU, hardMemory string, managed bool) *corev1.ResourceQuota {
	rq := &corev1.ResourceQuota{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespaceName,
		},
		Spec: corev1.ResourceQuotaSpec{
			Hard: corev1.ResourceList{
				corev1.ResourceLimitsCPU:    resource.MustParse(hardCPU),
				corev1.ResourceLimitsMemory: resource.MustParse(hardMemory),
			},
		},
	}
	if managed {
		rq.Labels = map[string]string{ManagedResourceQuotaLabel: "true"}
	}
	return rq
}

// NewLimitRange builds a container-default LimitRange, optionally carrying the Rancher-managed marker label.
func NewLimitRange(namespaceName, name string, managed bool) *corev1.LimitRange {
	lr := &corev1.LimitRange{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespaceName,
		},
		Spec: corev1.LimitRangeSpec{
			Limits: []corev1.LimitRangeItem{
				{
					Type: corev1.LimitTypeContainer,
					Default: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("512Mi"),
					},
					DefaultRequest: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("250m"),
						corev1.ResourceMemory: resource.MustParse("256Mi"),
					},
				},
			},
		},
	}
	if managed {
		lr.Labels = map[string]string{ManagedResourceQuotaLabel: "true"}
	}
	return lr
}

// WaitForManagedResourceQuotaHard polls until the single Rancher-managed ResourceQuota in the namespace has the expected hard limits.
func WaitForManagedResourceQuotaHard(client *rancher.Client, clusterID, namespaceName string, expected corev1.ResourceList) (*corev1.ResourceQuota, error) {
	var managed *corev1.ResourceQuota
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		list, err := extquotaapi.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{LabelSelector: ManagedResourceQuotaLabel + "=true"})
		if err != nil || len(list.Items) != 1 {
			return false, nil
		}
		rq := list.Items[0]
		for name, want := range expected {
			got, ok := rq.Spec.Hard[name]
			if !ok || want.Cmp(got) != 0 {
				return false, nil
			}
		}
		managed = rq.DeepCopy()
		return true, nil
	})
	return managed, err
}

// WaitForManagedResourceQuota polls until a Rancher-managed ResourceQuota exists in the namespace.
func WaitForManagedResourceQuota(client *rancher.Client, clusterID, namespaceName string) (*corev1.ResourceQuota, error) {
	var managed *corev1.ResourceQuota
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		list, err := extquotaapi.ListResourceQuotas(client, clusterID, namespaceName, metav1.ListOptions{LabelSelector: ManagedResourceQuotaLabel + "=true"})
		if err != nil || len(list.Items) != 1 {
			return false, nil
		}
		managed = list.Items[0].DeepCopy()
		return true, nil
	})
	return managed, err
}

// WaitForManagedLimitRange polls until a Rancher-managed LimitRange exists in the namespace.
func WaitForManagedLimitRange(client *rancher.Client, clusterID, namespaceName string) (*corev1.LimitRange, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	var managed *corev1.LimitRange
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		list, err := clusterContext.Core.LimitRange().List(namespaceName, metav1.ListOptions{LabelSelector: ManagedResourceQuotaLabel + "=true"})
		if err != nil || len(list.Items) != 1 {
			return false, nil
		}
		managed = list.Items[0].DeepCopy()
		return true, nil
	})
	return managed, err
}

// WaitForManagedLimitRangeDefault polls until the Rancher-managed LimitRange in the namespace has the expected container default limits.
func WaitForManagedLimitRangeDefault(client *rancher.Client, clusterID, namespaceName string, expectedCPU, expectedMemory resource.Quantity) (*corev1.LimitRange, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	var managed *corev1.LimitRange
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		list, err := clusterContext.Core.LimitRange().List(namespaceName, metav1.ListOptions{LabelSelector: ManagedResourceQuotaLabel + "=true"})
		if err != nil || len(list.Items) != 1 {
			return false, nil
		}
		lr := list.Items[0]
		if len(lr.Spec.Limits) == 0 {
			return false, nil
		}
		gotCPU, cpuOK := lr.Spec.Limits[0].Default[corev1.ResourceCPU]
		gotMemory, memOK := lr.Spec.Limits[0].Default[corev1.ResourceMemory]
		if !cpuOK || !memOK || expectedCPU.Cmp(gotCPU) != 0 || expectedMemory.Cmp(gotMemory) != 0 {
			return false, nil
		}
		managed = lr.DeepCopy()
		return true, nil
	})
	return managed, err
}

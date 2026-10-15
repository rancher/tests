package namespaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateNamespaceResourceQuotaAnnotation updates the resource quota annotation on a namespace
func UpdateNamespaceResourceQuotaAnnotation(client *rancher.Client, clusterID string, namespaceName string, existingLimits map[string]string, extendedLimits map[string]string) error {
	quotaStr, err := BuildResourceQuotaAnnotation(existingLimits, extendedLimits)
	if err != nil {
		return err
	}

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

// BuildResourceQuotaAnnotation returns the value for the field.cattle.io/resourceQuota annotation built from the given standard limits and extended limits.
func BuildResourceQuotaAnnotation(existingLimits map[string]string, extendedLimits map[string]string) (string, error) {
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
		return "", fmt.Errorf("marshal resource quota annotation: %w", err)
	}

	return string(quotaJSON), nil
}

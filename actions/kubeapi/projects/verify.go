package projects

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VerifyUsedProjectExtendedResourceQuota checks used project-level extended resource quotas.
func VerifyUsedProjectExtendedResourceQuota(client *rancher.Client, clusterID, projectName string, expectedUsed map[string]string) error {
	project, err := client.WranglerContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	usedExtended := project.Spec.ResourceQuota.UsedLimit.Extended

	for resource, expectedVal := range expectedUsed {
		actual, ok := usedExtended[resource]
		if !ok {
			return fmt.Errorf("resource %q not found in project usedLimit.extended", resource)
		}

		if actual != expectedVal {
			return fmt.Errorf("project quota used mismatch for %q: expected=%s actual=%s", resource, expectedVal, actual)
		}
	}

	return nil
}

// VerifyUsedProjectExistingResourceQuota checks used project-level resource quotas defined via existing fields, e.g. pods, limitsCpu, limitsMemory.
func VerifyUsedProjectExistingResourceQuota(client *rancher.Client, clusterID, projectName string, expectedUsed map[string]string) error {
	project, err := client.WranglerContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	used := project.Spec.ResourceQuota.UsedLimit

	for resource, expectedVal := range expectedUsed {
		switch resource {

		case "pods":
			if used.Pods != expectedVal {
				return fmt.Errorf("project quota used mismatch for %q: expected=%s actual=%s", resource, expectedVal, used.Pods)
			}

		case "limitsCpu":
			if used.LimitsCPU != expectedVal {
				return fmt.Errorf("project quota used mismatch for %q: expected=%s actual=%s", resource, expectedVal, used.LimitsCPU)
			}

		case "limitsMemory":
			if used.LimitsMemory != expectedVal {
				return fmt.Errorf("project quota used mismatch for %q: expected=%s actual=%s", resource, expectedVal, used.LimitsMemory)
			}

		default:
			return fmt.Errorf("unsupported existing project quota resource %q", resource)
		}
	}

	return nil
}

// VerifyProjectHasNoExtendedResourceQuota checks that the project has no extended resource quotas set.
func VerifyProjectHasNoExtendedResourceQuota(client *rancher.Client, clusterID, projectName string) error {
	project, err := client.WranglerContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if len(project.Spec.ResourceQuota.Limit.Extended) != 0 {
		return fmt.Errorf("expected no extended quota limits, found %+v", project.Spec.ResourceQuota.Limit.Extended)
	}

	if len(project.Spec.ResourceQuota.UsedLimit.Extended) != 0 {
		return fmt.Errorf("expected no extended quota usage, found %+v", project.Spec.ResourceQuota.UsedLimit.Extended)
	}

	return nil
}

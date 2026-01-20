package rbac

import (
	"context"
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateGlobalRole is a helper function that uses wrangler context to update an existing global role
func UpdateGlobalRole(client *rancher.Client, updatedGlobalRole *v3.GlobalRole) (*v3.GlobalRole, error) {
	var updated *v3.GlobalRole
	var lastErr error
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		current, getErr := client.WranglerContext.Mgmt.GlobalRole().Get(updatedGlobalRole.Name, metav1.GetOptions{})
		if getErr != nil {
			lastErr = fmt.Errorf("failed to get GlobalRole %s: %w", updatedGlobalRole.Name, getErr)
			return false, nil
		}

		updatedGlobalRole.ResourceVersion = current.ResourceVersion

		updated, lastErr = client.WranglerContext.Mgmt.GlobalRole().Update(updatedGlobalRole)
		if lastErr != nil {
			if errors.IsConflict(lastErr) {
				return false, nil
			}
			return false, lastErr
		}

		return true, nil
	},
	)

	if err != nil {
		return nil, fmt.Errorf("timed out updating GlobalRole %s: %w", updatedGlobalRole.Name, lastErr)
	}

	return updated, nil
}

// UpdateRoleTemplate is a helper function that uses wrangler context to update an existing role template
func UpdateRoleTemplate(client *rancher.Client, updatedRoleTemplate *v3.RoleTemplate) (*v3.RoleTemplate, error) {
	currentRoleTemplate, err := client.WranglerContext.Mgmt.RoleTemplate().Get(
		updatedRoleTemplate.Name,
		metav1.GetOptions{},
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get RoleTemplate %s: %w", updatedRoleTemplate.Name, err)
	}

	updatedRoleTemplate.ResourceVersion = currentRoleTemplate.ResourceVersion

	if _, err := client.WranglerContext.Mgmt.RoleTemplate().Update(updatedRoleTemplate); err != nil {
		return nil, fmt.Errorf("failed to update RoleTemplate %s: %w", updatedRoleTemplate.Name, err)
	}

	var newRoleTemplate *v3.RoleTemplate
	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		rt, err := client.WranglerContext.Mgmt.RoleTemplate().Get(updatedRoleTemplate.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		if rt.ResourceVersion != currentRoleTemplate.ResourceVersion {
			newRoleTemplate = rt
			return true, nil
		}

		return false, nil
	},
	)

	if err != nil {
		return nil, fmt.Errorf("timed out waiting for RoleTemplate %s to be updated: %w", updatedRoleTemplate.Name, err)
	}

	return newRoleTemplate, nil
}

// UpdateClusterRoleTemplateBindings is a helper function that uses the dynamic client to update an existing cluster role template binding
func UpdateClusterRoleTemplateBindings(client *rancher.Client, existingCRTB *v3.ClusterRoleTemplateBinding, updatedCRTB *v3.ClusterRoleTemplateBinding) (*v3.ClusterRoleTemplateBinding, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterapi.LocalCluster)
	if err != nil {
		return nil, err
	}
	crtbUnstructured := dynamicClient.Resource(ClusterRoleTemplateBindingGroupVersionResource).Namespace(existingCRTB.Namespace)
	clusterRoleTemplateBinding, err := crtbUnstructured.Get(context.TODO(), existingCRTB.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	currentCRTB := &v3.ClusterRoleTemplateBinding{}
	err = scheme.Scheme.Convert(clusterRoleTemplateBinding, currentCRTB, clusterRoleTemplateBinding.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	updatedCRTB.ObjectMeta.ResourceVersion = currentCRTB.ObjectMeta.ResourceVersion

	unstructuredResp, err := crtbUnstructured.Update(context.TODO(), unstructured.MustToUnstructured(updatedCRTB), metav1.UpdateOptions{})
	if err != nil {
		return nil, err
	}

	newCRTB := &v3.ClusterRoleTemplateBinding{}
	err = scheme.Scheme.Convert(unstructuredResp, newCRTB, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}
	return newCRTB, nil

}

// UpdateRoleTemplateInheritance updates the inheritance of a role template using wrangler context
func UpdateRoleTemplateInheritance(client *rancher.Client, roleTemplateName string, inheritedRoles []*v3.RoleTemplate) (*v3.RoleTemplate, error) {
	var roleTemplateNames []string
	for _, inheritedRole := range inheritedRoles {
		if inheritedRole != nil {
			roleTemplateNames = append(roleTemplateNames, inheritedRole.Name)
		}
	}

	existingRoleTemplate, err := GetRoleTemplateByName(client, roleTemplateName)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing RoleTemplate: %w", err)
	}

	existingRoleTemplate.RoleTemplateNames = roleTemplateNames

	updatedRoleTemplate, err := client.WranglerContext.Mgmt.RoleTemplate().Update(existingRoleTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to update RoleTemplate inheritance: %w", err)
	}

	return GetRoleTemplateByName(client, updatedRoleTemplate.Name)
}

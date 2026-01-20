package rbac

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// DeleteGlobalRole is a helper function to delete a Global Role by name using wrangler context
func DeleteGlobalRole(client *rancher.Client, globalRoleName string) error {
	err := client.WranglerContext.Mgmt.GlobalRole().Delete(globalRoleName, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete GlobalRole %s: %w", globalRoleName, err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = client.WranglerContext.Mgmt.GlobalRole().Get(globalRoleName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}

		if err != nil {
			return false, fmt.Errorf("error checking GlobalRole deletion status: %w", err)
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for GlobalRole %s to be deleted: %w", globalRoleName, err)
	}

	return nil
}

// DeleteClusterRoleTemplateBinding deletes the cluster role template binding using wrangler context
func DeleteClusterRoleTemplateBinding(client *rancher.Client, crtbNamespace, crtbName string) error {
	err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtbNamespace, crtbName, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete ClusterRoleTemplateBinding %s: %w", crtbName, err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveHundredMillisecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Get(crtbNamespace, crtbName, metav1.GetOptions{})

		if apierrors.IsNotFound(err) {
			return true, nil
		}

		if err != nil {
			return false, fmt.Errorf("error checking CRTB deletion status: %w", err)
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for ClusterRoleTemplateBinding %s to be deleted: %w", crtbName, err)
	}

	return nil
}

// DeleteProjectRoleTemplateBinding deletes the project role template binding using wrangler context
func DeleteProjectRoleTemplateBinding(client *rancher.Client, prtbNamespace, prtbName string) error {
	err := client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Delete(prtbNamespace, prtbName, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete ProjectRoleTemplateBinding %s: %w", prtbName, err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbNamespace, prtbName, metav1.GetOptions{})

		if apierrors.IsNotFound(err) {
			return true, nil
		}

		if err != nil {
			return false, fmt.Errorf("error checking PRTB deletion status: %w", err)
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for ProjectRoleTemplateBinding %s to be deleted: %w", prtbName, err)
	}

	return nil
}

// DeleteRoleTemplate deletes a role template by name using wrangler context
func DeleteRoleTemplate(client *rancher.Client, rtName string) error {
	err := client.WranglerContext.Mgmt.RoleTemplate().Delete(rtName, nil)
	if err != nil {
		return fmt.Errorf("failed to delete role template %s: %w", rtName, err)
	}

	err = WaitForRoleTemplateDeletion(client, rtName)
	if err != nil {
		return fmt.Errorf("role template %s not deleted in time: %w", rtName, err)
	}

	return nil
}

// WaitForRoleTemplateDeletion waits until the RoleTemplate is deleted
func WaitForRoleTemplateDeletion(client *rancher.Client, rtName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = client.WranglerContext.Mgmt.RoleTemplate().Get(rtName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		return false, nil
	},
	)
}

// DeleteGlobalRoleBinding deletes a global role binding by name using wrangler context
func DeleteGlobalRoleBinding(client *rancher.Client, globalRoleBindingName string) error {
	err := client.WranglerContext.Mgmt.GlobalRoleBinding().Delete(globalRoleBindingName, nil)
	if err != nil {
		return fmt.Errorf("failed to delete global role binding %s: %w", globalRoleBindingName, err)
	}

	err = WaitForGlobalRoleBindingDeletion(client, globalRoleBindingName)
	if err != nil {
		return fmt.Errorf("global role binding %s not deleted in time: %w", globalRoleBindingName, err)
	}

	return nil
}

// WaitForGlobalRoleBindingDeletion waits until the GlobalRoleBinding is deleted
func WaitForGlobalRoleBindingDeletion(client *rancher.Client, globalRoleBindingName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = client.WranglerContext.Mgmt.GlobalRoleBinding().Get(globalRoleBindingName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		return false, nil
	},
	)
}

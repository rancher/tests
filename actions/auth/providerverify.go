package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/clientbase"
	"github.com/rancher/tests/actions/rbac"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const allNamespaces = ""

// VerifyProviderDisabled waits for the auth config to report the provider disabled with its cleanup annotation locked
func VerifyProviderDisabled(client *rancher.Client, providerName string) error {
	authConfig, err := WaitForAuthProviderAnnotationUpdate(client, providerName, AuthProvCleanupAnnotationValLocked)
	if err != nil {
		return fmt.Errorf("timed out waiting for %s to report its cleanup annotation locked: %w", providerName, err)
	}

	if authConfig.Enabled {
		return fmt.Errorf("auth config %s still reports enabled after being disabled", providerName)
	}

	return nil
}

// VerifyProviderSessionRejected waits until a client authenticated through the provider can no longer reach the Rancher API
func VerifyProviderSessionRejected(authClient *rancher.Client) error {
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(context.Context) (bool, error) {
		_, listErr := authClient.Management.User.List(&types.ListOpts{})
		if listErr == nil {
			lastErr = fmt.Errorf("a session established through the provider still reaches the Rancher API after the provider was disabled")
			return false, nil
		}

		if isUnauthorized(listErr) {
			return true, nil
		}

		lastErr = fmt.Errorf("listing users through the provider session failed for an unrelated reason: %w", listErr)

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for the provider session to be rejected: %w", lastErr)
	}

	return nil
}

func isUnauthorized(err error) bool {
	var apiError *clientbase.APIError

	if errors.As(err, &apiError) {
		return apiError.StatusCode == http.StatusUnauthorized || apiError.StatusCode == http.StatusForbidden
	}

	return false
}

// VerifySecretDeleted waits for the provider's service account password secret to be removed from the global data namespace
func VerifySecretDeleted(client *rancher.Client, secretID string) error {
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(context.Context) (bool, error) {
		_, getErr := client.WranglerContext.Core.Secret().Get(rbac.GlobalDataNS, secretID, metav1.GetOptions{})
		if apierrors.IsNotFound(getErr) {
			return true, nil
		}

		if getErr != nil {
			lastErr = getErr
			return false, nil
		}

		lastErr = fmt.Errorf("secret %s/%s still exists", rbac.GlobalDataNS, secretID)

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("service account password secret was not cleaned up: %w", lastErr)
	}

	return nil
}

// VerifyBindingsDeleted waits until no global, cluster or project role binding still references a principal emitted by the given provider
func VerifyBindingsDeleted(client *rancher.Client, providerName string) error {
	prefix := providerName + "_"

	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(context.Context) (bool, error) {
		remaining, checkErr := remainingBindings(client, prefix)
		if checkErr != nil {
			lastErr = checkErr
			return false, nil
		}

		if len(remaining) > 0 {
			lastErr = fmt.Errorf("bindings still reference %s principals: %s", providerName, strings.Join(remaining, ", "))
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return fmt.Errorf("role bindings were not cleaned up: %w", lastErr)
	}

	return nil
}

func remainingBindings(client *rancher.Client, prefix string) ([]string, error) {
	var remaining []string

	grbs, err := client.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list global role bindings: %w", err)
	}

	for _, grb := range grbs.Items {
		if strings.HasPrefix(grb.GroupPrincipalName, prefix) || strings.HasPrefix(grb.UserPrincipalName, prefix) {
			remaining = append(remaining, "globalrolebinding/"+grb.Name)
		}
	}

	crtbs, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().List(allNamespaces, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role template bindings: %w", err)
	}

	for _, crtb := range crtbs.Items {
		if strings.HasPrefix(crtb.GroupPrincipalName, prefix) || strings.HasPrefix(crtb.UserPrincipalName, prefix) {
			remaining = append(remaining, "clusterroletemplatebinding/"+crtb.Name)
		}
	}

	prtbs, err := client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List(allNamespaces, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list project role template bindings: %w", err)
	}

	for _, prtb := range prtbs.Items {
		if strings.HasPrefix(prtb.GroupPrincipalName, prefix) || strings.HasPrefix(prtb.UserPrincipalName, prefix) {
			remaining = append(remaining, "projectroletemplatebinding/"+prtb.Name)
		}
	}

	return remaining, nil
}

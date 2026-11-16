package authconfigs

import (
	"context"
	"fmt"

	cattlev3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

const (
	logoutAllEnabledField = "logoutAllEnabled"
	logoutAllForcedField  = "logoutAllForced"
)

var authConfigResource = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "authconfigs"}

// EnsureAuthConfigExists creates the named auth config of the given type when the Rancher server holds none
func EnsureAuthConfigExists(client *rancher.Client, name, configType string) error {
	_, err := client.WranglerContext.Mgmt.AuthConfig().Get(name, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	if !k8serrors.IsNotFound(err) {
		return fmt.Errorf("failed to get the %s auth config: %w", name, err)
	}

	logrus.Infof("Creating the %s auth config, which this Rancher server does not hold", name)
	_, err = client.WranglerContext.Mgmt.AuthConfig().Create(&cattlev3.AuthConfig{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Type:       configType,
	})
	if err != nil && !k8serrors.IsAlreadyExists(err) {
		return fmt.Errorf("failed to create the %s auth config: %w", name, err)
	}

	return nil
}

// AuthConfigEnabled reports whether the named auth config is enabled, and false when the Rancher server holds none
func AuthConfigEnabled(client *rancher.Client, name string) (bool, error) {
	authConfig, err := client.WranglerContext.Mgmt.AuthConfig().Get(name, metav1.GetOptions{})
	if k8serrors.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("failed to get the %s auth config: %w", name, err)
	}

	return authConfig.Enabled, nil
}

// FindLogoutAllSettings returns whether single logout is enabled and forced on the named auth config
func FindLogoutAllSettings(client *rancher.Client, name string) (bool, bool, error) {
	dynamicClient, err := client.GetRancherDynamicClient()
	if err != nil {
		return false, false, err
	}

	authConfig, err := dynamicClient.Resource(authConfigResource).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return false, false, fmt.Errorf("getting the %s auth config: %w", name, err)
	}

	enabled, _, err := unstructured.NestedBool(authConfig.Object, logoutAllEnabledField)
	if err != nil {
		return false, false, fmt.Errorf("reading %s from the %s auth config: %w", logoutAllEnabledField, name, err)
	}

	forced, _, err := unstructured.NestedBool(authConfig.Object, logoutAllForcedField)
	if err != nil {
		return false, false, fmt.Errorf("reading %s from the %s auth config: %w", logoutAllForcedField, name, err)
	}

	return enabled, forced, nil
}

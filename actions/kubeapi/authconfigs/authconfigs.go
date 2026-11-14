package authconfigs

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	cattlev3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/sirupsen/logrus"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
)

const (
	UserIDAttributeField  = "userIDAttribute"
	GroupIDAttributeField = "groupIDAttribute"
	AccessModeField       = "accessMode"
)

var authConfigResource = schema.GroupVersionResource{Group: "management.cattle.io", Version: "v3", Resource: "authconfigs"}

// PatchAuthConfig applies a JSON merge patch to the named auth config through the Kubernetes API
func PatchAuthConfig(client *rancher.Client, name string, patch map[string]any) error {
	body, err := json.Marshal(patch)
	if err != nil {
		return fmt.Errorf("encoding the patch for the %s auth config: %w", name, err)
	}

	if _, err := client.WranglerContext.Mgmt.AuthConfig().Patch(name, types.MergePatchType, body); err != nil {
		return fmt.Errorf("patching the %s auth config: %w", name, err)
	}

	return nil
}

// FindAuthConfigField returns the string field at the given path of the named auth config and whether it is set
func FindAuthConfigField(client *rancher.Client, name string, path ...string) (string, bool, error) {
	dynamicClient, err := client.GetRancherDynamicClient()
	if err != nil {
		return "", false, err
	}

	authConfig, err := dynamicClient.Resource(authConfigResource).Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		return "", false, fmt.Errorf("getting the %s auth config: %w", name, err)
	}

	value, found, err := unstructured.NestedString(authConfig.Object, path...)
	if err != nil {
		return "", false, fmt.Errorf("reading %s from the %s auth config: %w", strings.Join(path, "."), name, err)
	}

	return value, found, nil
}

// FindAuthConfigIDAttributes returns the principal ID attributes stored on the named LDAP-based auth config
func FindAuthConfigIDAttributes(client *rancher.Client, name string) (string, string, error) {
	userIDAttribute, _, err := FindAuthConfigField(client, name, UserIDAttributeField)
	if err != nil {
		return "", "", err
	}

	groupIDAttribute, _, err := FindAuthConfigField(client, name, GroupIDAttributeField)
	if err != nil {
		return "", "", err
	}

	return userIDAttribute, groupIDAttribute, nil
}

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

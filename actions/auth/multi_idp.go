package auth

import (
	"context"
	"fmt"
	"slices"
	"strings"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	shepherdauth "github.com/rancher/shepherd/clients/rancher/auth"
	"github.com/rancher/shepherd/clients/rancher/auth/openldap"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authnv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	MultiIdpAuthInput = "multiIdpAuthInput"
	MultiAuthProvidersFeature     = "multiple-auth-providers"
	MultiAuthProviderCrossFeature = "multiple-auth-provider-cross"
	OpenLdapConfigType = "openLdapConfig"
	OpenLdapSecretField = "serviceaccountpassword"
	secretNameSeparator = "-"
)

// NamedOpenLdapInstance describes one OpenLDAP auth config, the directory behind it, and the accounts in that directory
type NamedOpenLdapInstance struct {
	ConfigName  string           `yaml:"configName"`
	DisplayName string           `yaml:"displayName"`
	OpenLdap    *openldap.Config `yaml:"openLDAP"`
	Directory   AuthConfig       `yaml:"directory"`
}

// MultiIdpInput is the multiIdpAuthInput config
type MultiIdpInput struct {
	OpenLdap []NamedOpenLdapInstance `yaml:"openLdap"`
}

// LoadMultiIdpInput reads the multiIdpAuthInput config
func LoadMultiIdpInput() *MultiIdpInput {
	input := new(MultiIdpInput)
	config.LoadConfig(MultiIdpAuthInput, input)

	return input
}

// Validate returns an error if the instance cannot be enabled or logged into
func (n *NamedOpenLdapInstance) Validate() error {
	if n.ConfigName == "" {
		return fmt.Errorf("an openLdap instance in %s has no configName", MultiIdpAuthInput)
	}

	if n.ConfigName == OpenLdap {
		return fmt.Errorf("instance %q uses the provider's default config name, name it something else", n.ConfigName)
	}

	if n.OpenLdap == nil || n.OpenLdap.Users == nil || n.OpenLdap.Users.Admin == nil ||
		n.OpenLdap.Groups == nil || n.OpenLdap.ServiceAccount == nil {
		return fmt.Errorf("instance %q needs openLDAP users.admin, groups and serviceAccount", n.ConfigName)
	}

	return nil
}

// UserSearchBase returns the search base of the instance's users
func (n *NamedOpenLdapInstance) UserSearchBase() string {
	return n.OpenLdap.Users.SearchBase
}

// GroupSearchBase returns the search base of the instance's groups
func (n *NamedOpenLdapInstance) GroupSearchBase() string {
	return n.OpenLdap.Groups.SearchBase
}

// AdminUser returns the account the instance is enabled and administered with
func (n *NamedOpenLdapInstance) AdminUser() *v3.User {
	return &v3.User{Username: n.OpenLdap.Users.Admin.Username, Password: n.OpenLdap.Users.Admin.Password}
}

// UserPrincipalID builds the principal ID of a user of the instance
func (n *NamedOpenLdapInstance) UserPrincipalID(username string) string {
	return NewPrincipalID(n.ConfigName, PrincipalTypeUser, username, n.UserSearchBase(), n.GroupSearchBase())
}

// GroupPrincipalID builds the principal ID of a group of the instance
func (n *NamedOpenLdapInstance) GroupPrincipalID(groupName string) string {
	return NewPrincipalID(n.ConfigName, PrincipalTypeGroup, groupName, n.UserSearchBase(), n.GroupSearchBase())
}

// PrincipalPrefix is what every user principal ID issued by the instance starts with
func (n *NamedOpenLdapInstance) PrincipalPrefix() string {
	return n.ConfigName + "_" + PrincipalTypeUser + "://"
}

// NamedOpenLdapClient returns the OpenLDAP client bound to the instance's auth config
func NamedOpenLdapClient(client *rancher.Client, instance *NamedOpenLdapInstance) *openldap.OLDAPClient {
	return client.Auth.OLDAP.ForConfig(instance.ConfigName, instance.DisplayName, instance.OpenLdap)
}

// createNamedAuthConfig creates the instance's AuthConfig through Wrangler
func createNamedAuthConfig(client *rancher.Client, instance *NamedOpenLdapInstance) error {
	authConfigs := client.WranglerContext.Mgmt.AuthConfig()

	_, err := authConfigs.Get(instance.ConfigName, metav1.GetOptions{})
	if err == nil {
		return nil
	}

	if !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to look up auth config %s: %w", instance.ConfigName, err)
	}

	_, err = authConfigs.Create(&managementv3.AuthConfig{
		ObjectMeta: metav1.ObjectMeta{
			Name:        instance.ConfigName,
			Annotations: map[string]string{AuthProvCleanupAnnotationKey: AuthProvCleanupAnnotationValLocked},
		},
		Type: OpenLdapConfigType,
	})
	if err != nil {
		return fmt.Errorf("failed to create auth config %s: %w", instance.ConfigName, err)
	}

	return nil
}

// EnsureNamedOpenLdapEnabled creates and enables the instance's auth config and waits for it to unlock, doing nothing if it is already enabled
func EnsureNamedOpenLdapEnabled(client *rancher.Client, instance *NamedOpenLdapInstance) error {
	existing, err := client.Management.AuthConfig.ByID(instance.ConfigName)
	if err == nil && existing.Enabled {
		return nil
	}

	if err := createNamedAuthConfig(client, instance); err != nil {
		return err
	}

	if err := NamedOpenLdapClient(client, instance).Enable(); err != nil {
		return fmt.Errorf("failed to enable auth config %s: %w", instance.ConfigName, err)
	}

	enabled, err := WaitForAuthProviderAnnotationUpdate(client, instance.ConfigName, AuthProvCleanupAnnotationValUnlocked)
	if err != nil {
		return err
	}

	if instance.DisplayName != "" && enabled.DisplayName != instance.DisplayName {
		updates := *enabled
		updates.DisplayName = instance.DisplayName

		if _, err := client.Auth.OLDAP.Update(enabled, &updates); err != nil {
			return fmt.Errorf("failed to set the display name of %s: %w", instance.ConfigName, err)
		}
	}

	return nil
}

// DisableNamedOpenLdap disables the instance's auth config and waits for it to lock
func DisableNamedOpenLdap(client *rancher.Client, instance *NamedOpenLdapInstance) error {
	if err := NamedOpenLdapClient(client, instance).Disable(); err != nil {
		return fmt.Errorf("failed to disable auth config %s: %w", instance.ConfigName, err)
	}

	_, err := WaitForAuthProviderAnnotationUpdate(client, instance.ConfigName, AuthProvCleanupAnnotationValLocked)

	return err
}

// DeleteNamedOpenLdap disables and deletes the instance's auth config, treating one that is already gone as deleted
func DeleteNamedOpenLdap(client *rancher.Client, instance *NamedOpenLdapInstance) error {
	if _, err := client.WranglerContext.Mgmt.AuthConfig().Get(instance.ConfigName, metav1.GetOptions{}); err != nil {
		if apierrors.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("failed to look up auth config %s: %w", instance.ConfigName, err)
	}

	if err := DisableNamedOpenLdap(client, instance); err != nil {
		return err
	}

	err := client.WranglerContext.Mgmt.AuthConfig().Delete(instance.ConfigName, &metav1.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		return fmt.Errorf("failed to delete auth config %s: %w", instance.ConfigName, err)
	}

	return nil
}

// SetupNamedOpenLdapSession enables the instance, signs its administrator in through it, and returns a new session and client
func SetupNamedOpenLdapSession(client *rancher.Client, baseSession *session.Session, instance *NamedOpenLdapInstance) (*session.Session, *rancher.Client, error) {
	if err := EnsureNamedOpenLdapEnabled(client, instance); err != nil {
		return nil, nil, err
	}

	authSession := baseSession.NewSession()

	newClient, err := client.WithSession(authSession)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client with new session: %w", err)
	}

	authAdmin, err := LoginAsNamedAuthUser(newClient, instance.AdminUser(), instance.ConfigName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate admin through %s: %w", instance.ConfigName, err)
	}

	return authSession, authAdmin, nil
}

// LoginAsNamedAuthUser signs a user in through the auth config with the given name and returns an authenticated client
func LoginAsNamedAuthUser(client *rancher.Client, user *v3.User, configName string) (*rancher.Client, error) {
	enabled := true
	user.Enabled = &enabled

	return client.AsNamedAuthUser(user, shepherdauth.Provider(OpenLdap), configName)
}

// VerifyNamedUserLogins signs each user in through the named auth config and verifies the login succeeds or fails as expected
func VerifyNamedUserLogins(client *rancher.Client, configName string, users []User, description string, shouldSucceed bool) error {
	for _, userInfo := range users {
		user := &v3.User{Username: userInfo.Username, Password: userInfo.Password}

		_, err := LoginAsNamedAuthUser(client, user, configName)

		if shouldSucceed && err != nil {
			return fmt.Errorf("user [%v] should be able to login through %s (%s): %w", userInfo.Username, configName, description, err)
		}

		if !shouldSucceed && err == nil {
			return fmt.Errorf("user [%v] should NOT be able to login through %s (%s)", userInfo.Username, configName, description)
		}
	}

	return nil
}

// UpdateNamedAccessMode sets the access mode and allowed principals of the named auth config
func UpdateNamedAccessMode(client *rancher.Client, configName, accessMode string, allowedPrincipalIDs []string) (*v3.AuthConfig, error) {
	existing, updates, err := NewAuthConfigWithAccessMode(client, configName, accessMode, allowedPrincipalIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare auth config %s with access mode %s: %w", configName, accessMode, err)
	}

	return client.Auth.OLDAP.Update(existing, updates)
}

// NamedConfigSecretID mirrors how Rancher names the secrets of an auth config: the provider's default config keeps the type based name
func NamedConfigSecretID(configName, providerName, configType, field string) string {
	prefix := strings.ToLower(configType)
	if configName != "" && configName != providerName {
		prefix += secretNameSeparator + strings.ToLower(configName)
	}

	return prefix + secretNameSeparator + field
}

// UserAttributeConfigKeys returns the auth config names a user's UserAttribute stores groups and extras under
func UserAttributeConfigKeys(client *rancher.Client, userID string) (groupKeys, extraKeys []string, err error) {
	attributes, err := client.WranglerContext.Mgmt.UserAttribute().Get(userID, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get the user attribute of %s: %w", userID, err)
	}

	for key := range attributes.GroupPrincipals {
		groupKeys = append(groupKeys, key)
	}

	for key := range attributes.ExtraByProvider {
		extraKeys = append(extraKeys, key)
	}

	slices.Sort(groupKeys)
	slices.Sort(extraKeys)

	return groupKeys, extraKeys, nil
}

// UserAttributeEntriesUnderKey returns the group and extra principal IDs stored under a provider key, so a test can check what was written rather than whether the key exists
func UserAttributeEntriesUnderKey(client *rancher.Client, userID, key string) (groupIDs, extraIDs []string, err error) {
	attributes, err := client.WranglerContext.Mgmt.UserAttribute().Get(userID, metav1.GetOptions{})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get the user attribute of %s: %w", userID, err)
	}

	for _, group := range attributes.GroupPrincipals[key].Items {
		groupIDs = append(groupIDs, group.Name)
	}

	extraIDs = append(extraIDs, attributes.ExtraByProvider[key]["principalid"]...)

	return groupIDs, extraIDs, nil
}

// proxyRestConfig returns a rest config for the Rancher proxy context of a generated kubeconfig
func proxyRestConfig(generated string) (*rest.Config, error) {
	raw, err := clientcmd.Load([]byte(generated))
	if err != nil {
		return nil, fmt.Errorf("parsing the generated kubeconfig: %w", err)
	}

	for name, kubeContext := range raw.Contexts {
		cluster, found := raw.Clusters[kubeContext.Cluster]
		if !found || !strings.Contains(cluster.Server, rancherProxyPath) {
			continue
		}

		return clientcmd.NewNonInteractiveClientConfig(*raw, name, &clientcmd.ConfigOverrides{}, nil).ClientConfig()
	}

	return nil, fmt.Errorf("the generated kubeconfig names no Rancher proxy context")
}

// RancherRequestGroups returns the groups the cluster resolves for a request the user makes through the Rancher proxy
func RancherRequestGroups(userClient *rancher.Client, clusterID string) ([]string, error) {
	generated, err := aceKubeconfig(userClient, clusterID)
	if err != nil {
		return nil, err
	}

	restConfig, err := proxyRestConfig(generated)
	if err != nil {
		return nil, err
	}

	clientset, err := kubernetes.NewForConfig(restConfig)
	if err != nil {
		return nil, fmt.Errorf("building a clientset for the Rancher proxy context: %w", err)
	}

	review, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(context.Background(), &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
	if err != nil {
		return nil, fmt.Errorf("asking the cluster who it thinks it is talking to: %w", err)
	}

	return review.Status.UserInfo.Groups, nil
}

// WaitForAuthConfigDeleted polls until the named auth config no longer exists
func WaitForAuthConfigDeleted(client *rancher.Client, configName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true, func(context.Context) (bool, error) {
		_, err := client.WranglerContext.Mgmt.AuthConfig().Get(configName, metav1.GetOptions{})

		return apierrors.IsNotFound(err), nil
	})
}

// UserAttributeGroupPrincipalIDs returns the IDs of the groups a user's UserAttribute stores under the config
func UserAttributeGroupPrincipalIDs(client *rancher.Client, userID, configName string) ([]string, error) {
	attributes, err := client.WranglerContext.Mgmt.UserAttribute().Get(userID, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get the user attribute of %s: %w", userID, err)
	}

	var ids []string
	for _, group := range attributes.GroupPrincipals[configName].Items {
		ids = append(ids, group.Name)
	}

	return ids, nil
}

// WaitForUserAttributeConfigKeyRemoved refreshes every user's provider access on each poll until the user's UserAttribute no longer holds groups or extras for the config.
// The client must be a Rancher administrator.
func WaitForUserAttributeConfigKeyRemoved(client *rancher.Client, userID, configName string) error {
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FiveMinuteTimeout, true, func(context.Context) (bool, error) {
		if err := users.RefreshGroupMembership(client); err != nil {
			lastErr = err
			return false, nil
		}

		groupKeys, extraKeys, err := UserAttributeConfigKeys(client, userID)
		if err != nil {
			lastErr = err
			return false, nil
		}

		return !slices.Contains(groupKeys, configName) && !slices.Contains(extraKeys, configName), nil
	})
	if err != nil {
		return fmt.Errorf("%s is still in the user attribute of %s, last error %v: %w", configName, userID, lastErr, err)
	}

	return nil
}

// VerifyProviderTypesInclude verifies Rancher's public auth provider types list names every given provider ID
func VerifyProviderTypesInclude(client *rancher.Client, providerIDs ...string) error {
	providerTypes, err := client.Auth.ListProviderTypes()
	if err != nil {
		return err
	}

	listed := make([]string, 0, len(providerTypes))
	for _, providerType := range providerTypes {
		if id, ok := providerType["id"].(string); ok {
			listed = append(listed, id)
		}
	}

	for _, providerID := range providerIDs {
		if !slices.Contains(listed, providerID) {
			return fmt.Errorf("auth provider types %v do not include %q", listed, providerID)
		}
	}

	return nil
}

// WaitForPrincipalSearchPrefix polls the principal search for name until a returned principal ID does (or does not) start with prefix
func WaitForPrincipalSearchPrefix(client *rancher.Client, name, prefix string, wantPresent bool) error {
	var lastIDs []string
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(context.Context) (bool, error) {
		principals, err := searchPrincipals(client, name, "")
		if err != nil {
			lastErr = err
			return false, nil
		}

		lastIDs = lastIDs[:0]
		found := false

		for i := range principals {
			lastIDs = append(lastIDs, principals[i].ID)
			if strings.HasPrefix(principals[i].ID, prefix) {
				found = true
			}
		}

		return found == wantPresent, nil
	})
	if err != nil {
		return fmt.Errorf("principal search for %q: expected a principal with prefix %q present=%v, got %v, last error %v: %w", name, prefix, wantPresent, lastIDs, lastErr, err)
	}

	return nil
}

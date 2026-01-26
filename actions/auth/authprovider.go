package auth

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	AuthProvCleanupAnnotationKey         = "management.cattle.io/auth-provider-cleanup"
	AuthProvCleanupAnnotationValLocked   = "rancher-locked"
	AuthProvCleanupAnnotationValUnlocked = "unlocked"
	OpenLdapAuthInput                    = "openLdapAuthInput"
	ActiveDirectoryAuthInput             = "activeDirectoryAuthInput"
	AccessModeUnrestricted               = "unrestricted"
	AccessModeRestricted                 = "restricted"
	AccessModeRequired                   = "required"
	OpenLdap                             = "openldap"
	ActiveDirectory                      = "activedirectory"
	OpenLdapPasswordSecretID             = "openldapconfig-serviceaccountpassword"
	ActiveDirectoryPasswordSecretID      = "activedirectoryconfig-serviceaccountpassword"
)

type User struct {
	Username string
	Password string
}

// AuthConfig is a generic struct for auth provider configuration
type AuthConfig struct {
	Group             string `yaml:"group"`
	Users             []User `yaml:"users"`
	NestedGroup       string `yaml:"nestedGroup"`
	NestedUsers       []User `yaml:"nestedUsers"`
	DoubleNestedGroup string `yaml:"doubleNestedGroup"`
	DoubleNestedUsers []User `yaml:"doubleNestedUsers"`
	TripleNestedGroup string `yaml:"tripleNestedGroup"`
	TripleNestedUsers []User `yaml:"tripleNestedUsers"`
}

// SetupAuthenticatedSession enables the auth provider, logs in as the admin user, and returns a new session and client
func SetupAuthenticatedSession(client *rancher.Client, session *session.Session, adminUser *v3.User, providerName string) (*session.Session, *rancher.Client, error) {
	err := EnsureAuthProviderEnabled(client, providerName)
	if err != nil {
		return nil, nil, err
	}
	authSession := session.NewSession()
	newClient, err := client.WithSession(authSession)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client with new session: %w", err)
	}
	authAdmin, err := LoginAsAuthUser(newClient, adminUser, providerName)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate admin: %w", err)
	}
	return authSession, authAdmin, nil
}

// WaitForAuthProviderAnnotationUpdate polls the auth config until the cleanup annotation reaches the expected value
func WaitForAuthProviderAnnotationUpdate(client *rancher.Client, providerName, expectedAnnotation string) (*v3.AuthConfig, error) {
	var authConfig *v3.AuthConfig

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.FiveMinuteTimeout, false, func(context.Context) (bool, error) {
		newConfig, err := client.Management.AuthConfig.ByID(providerName)
		if err != nil {
			return false, nil
		}

		if newConfig.Annotations == nil {
			return false, nil
		}

		val, ok := newConfig.Annotations[AuthProvCleanupAnnotationKey]
		if ok && val == expectedAnnotation {
			authConfig = newConfig
			return true, nil
		}
		return false, nil
	})

	if err != nil {
		return nil, err
	}

	return authConfig, nil
}

// LoginAsAuthUser authenticates a user with the specified auth provider and returns an authenticated client
func LoginAsAuthUser(client *rancher.Client, user *v3.User, providerName string) (*rancher.Client, error) {
	var userEnabled = true
	user.Enabled = &userEnabled
	return client.AsAuthUser(user, auth.Provider(providerName))
}

// NewPrincipalID constructs a principal ID string in the format required by AD authentication
func NewPrincipalID(authConfigID, principalType, name, userSearchBase, groupSearchBase string) string {
	baseDN := userSearchBase

	if principalType == "group" {
		baseDN = groupSearchBase
	}

	cnAttribute := "cn"
	if authConfigID == ActiveDirectory {
		cnAttribute = "CN"
	}
	return fmt.Sprintf("%s_%s://%s=%s,%s", authConfigID, principalType, cnAttribute, name, baseDN)
}

// NewAuthConfigWithAccessMode retrieves the current auth config and returns both the existing config and an updated version with the specified access mode
func NewAuthConfigWithAccessMode(client *rancher.Client, authConfigID, accessMode string, allowedPrincipalIDs []string) (existing, updates *v3.AuthConfig, err error) {

	existing, err = client.Management.AuthConfig.ByID(authConfigID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to retrieve auth config: %w", err)
	}

	copyVal := *existing
	updates = &copyVal

	updates.AccessMode = accessMode

	if allowedPrincipalIDs != nil {
		updates.AllowedPrincipalIDs = append([]string{}, allowedPrincipalIDs...)
	} else {
		updates.AllowedPrincipalIDs = append([]string{}, existing.AllowedPrincipalIDs...)
	}

	return existing, updates, nil
}

// VerifyUserLogins attempts to authenticate each user in the provided list and verifies that the login succeeds or fails as expected
func VerifyUserLogins(authAdmin *rancher.Client, providerName string, users []User, description string, shouldSucceed bool) error {
	for _, userInfo := range users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}

		_, err := LoginAsAuthUser(authAdmin, user, providerName)

		if shouldSucceed && err != nil {
			return fmt.Errorf("user [%v] should be able to login (%s): %w", userInfo.Username, description, err)
		}

		if !shouldSucceed && err == nil {
			return fmt.Errorf("user [%v] should NOT be able to login (%s)", userInfo.Username, description)
		}
	}

	return nil
}

// EnsureAuthProviderEnabled enables the specified auth provider on the Rancher client.
func EnsureAuthProviderEnabled(client *rancher.Client, providerName string) error {
	switch providerName {
	case OpenLdap:
		return client.Auth.OLDAP.Enable()
	case ActiveDirectory:
		return client.Auth.ActiveDirectory.Enable()
	default:
		return fmt.Errorf("unsupported auth provider: %s", providerName)
	}
}

// WaitForNamespaceReady polls until the namespace is available within the specified timeout
func WaitForNamespaceReady(client *rancher.Client, namespaceName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
		if err != nil {
			if errors.IsNotFound(err) {
				return false, nil
			}
			return false, fmt.Errorf("failed to get namespace %s: %w", namespaceName, err)
		}
		return true, nil
	})
}

// GetGroupPrincipalID constructs a group principal ID using the provider's configuration
func GetGroupPrincipalID(providerName, groupName, userSearchBase, groupSearchBase string) string {
	return NewPrincipalID(providerName, "group", groupName, userSearchBase, groupSearchBase)
}

// GetUserPrincipalID constructs a user principal ID using the provider's configuration
func GetUserPrincipalID(providerName, username, userSearchBase, groupSearchBase string) string {
	return NewPrincipalID(providerName, "user", username, userSearchBase, groupSearchBase)
}

// UpdateAccessMode updates the auth config to the specified access mode with optional allowed principal IDs
func UpdateAccessMode(client *rancher.Client, providerName, accessMode string, allowedPrincipalIDs []string) (*v3.AuthConfig, error) {
	existing, updates, err := NewAuthConfigWithAccessMode(client, providerName, accessMode, allowedPrincipalIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare auth config with access mode %s: %w", accessMode, err)
	}
	var updatedConfig *v3.AuthConfig
	switch providerName {
	case OpenLdap:
		updatedConfig, err = client.Auth.OLDAP.Update(existing, updates)
	case ActiveDirectory:
		updatedConfig, err = client.Auth.ActiveDirectory.Update(existing, updates)
	default:
		return nil, fmt.Errorf("unsupported auth provider for update: %s", providerName)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to update auth config to access mode %s: %w", accessMode, err)
	}
	return updatedConfig, nil
}

// SetupRequiredAccessModePrincipals creates cluster role binding and prepares principal IDs for required access mode tests
func SetupRequiredAccessModePrincipals(authAdmin *rancher.Client, clusterID string, authConfig *AuthConfig, providerName, userSearchBase, groupSearchBase string) ([]string, error) {
	groupPrincipalID := GetGroupPrincipalID(providerName, authConfig.Group, userSearchBase, groupSearchBase)
	_, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, clusterID, groupPrincipalID, rbac.ClusterMember.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster role binding: %w", err)
	}

	principalIDs := []string{groupPrincipalID}

	for _, v := range authConfig.Users {
		userPrincipal := GetUserPrincipalID(providerName, v.Username, userSearchBase, groupSearchBase)
		principalIDs = append(principalIDs, userPrincipal)
	}
	return principalIDs, nil
}

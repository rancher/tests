// Package activedirectory provides utilities and test helpers for the Active Directory test automation
package activedirectory

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// Package-level constants used by the Active Directory test helpers.
const (
	passwordSecretID                     = "cattle-global-data/activedirectoryconfig-serviceaccountpassword"
	authProvCleanupAnnotationKey         = "management.cattle.io/auth-provider-cleanup"
	authProvCleanupAnnotationValLocked   = "rancher-locked"
	authProvCleanupAnnotationValUnlocked = "unlocked"
	// ConfigurationFileKey is used to load Active Directory auth configuration from test input.
	ConfigurationFileKey = "activeDirectoryAuthInput"
	activeDirectory      = "activedirectory"

	AccessModeUnrestricted = "unrestricted"
	AccessModeRestricted   = "restricted"
	AccessModeRequired     = "required"
)

// User represents credentials for an Active Directory test user.
type User struct {
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
}

// AuthConfig holds Active Directory test configuration values used by the suite.
type AuthConfig struct {
	StandardUser          string `json:"standardUser,omitempty" yaml:"standardUser,omitempty"`
	Groups                string `json:"group,omitempty" yaml:"group,omitempty"`
	Users                 []User `json:"users,omitempty" yaml:"users,omitempty"`
	ProjectGroup          string `json:"projectGroup,omitempty" yaml:"projectGroup,omitempty"`
	ProjectUsers          []User `json:"projectUsers,omitempty" yaml:"projectUsers,omitempty"`
	AllowedGroup          string `json:"allowedGroup,omitempty" yaml:"allowedGroup,omitempty"`
	AllowedGroupUsers     []User `json:"allowedGroupUsers,omitempty" yaml:"allowedGroupUsers,omitempty"`
	DisallowedGroup       string `json:"disallowedGroup,omitempty" yaml:"disallowedGroup,omitempty"`
	DisallowedGroupUsers  []User `json:"disallowedGroupUsers,omitempty" yaml:"disallowedGroupUsers,omitempty"`
	NestedGroup           string `json:"nestedGroup,omitempty" yaml:"nestedGroup,omitempty"`
	NestedUsers           []User `json:"nestedUsers,omitempty" yaml:"nestedUsers,omitempty"`
	DoubleNestedGroup     string `json:"doubleNestedGroup,omitempty" yaml:"doubleNestedGroup,omitempty"`
	DoubleNestedUsers     []User `json:"doubleNestedUsers,omitempty" yaml:"doubleNestedUsers,omitempty"`
	TripleNestedGroup     string `json:"tripleNestedGroup,omitempty" yaml:"tripleNestedGroup,omitempty"`
	TripleNestedUsers     []User `json:"tripleNestedUsers,omitempty" yaml:"tripleNestedUsers,omitempty"`
	DeepestNestedGroup    string `json:"deepestNestedGroup,omitempty" yaml:"deepestNestedGroup,omitempty"`
	DeepestNestedUsers    []User `json:"deepestNestedUsers,omitempty" yaml:"deepestNestedUsers,omitempty"`
	ClusterUsers          []User `json:"clusterUsers,omitempty" yaml:"clusterUsers,omitempty"`
	ProjectDirectUsers    []User `json:"projectDirectUsers,omitempty" yaml:"projectDirectUsers,omitempty"`
	AllowedUsers          []User `json:"allowedUsers,omitempty" yaml:"allowedUsers,omitempty"`
	DisallowedUsers       []User `json:"disallowedUsers,omitempty" yaml:"disallowedUsers,omitempty"`
	SpecialCharUser       User   `json:"specialCharUser,omitempty" yaml:"specialCharUser,omitempty"`
	SpecialCharGroup      string `json:"specialCharGroup,omitempty" yaml:"specialCharGroup,omitempty"`
	SpecialCharGroupUsers []User `json:"specialCharGroupUsers,omitempty" yaml:"specialCharGroupUsers,omitempty"`
	SpecialDNUsers        []User `json:"specialDNUsers,omitempty" yaml:"specialDNUsers,omitempty"`
}

// waitForAuthProviderAnnotationUpdate polls the auth config until the cleanup annotation reaches the expected value
func waitForAuthProviderAnnotationUpdate(client *rancher.Client, expectedAnnotation string) (*v3.AuthConfig, error) {
	var adConfig *v3.AuthConfig

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveHundredMillisecondTimeout, defaults.TwoMinuteTimeout, true, func(context.Context) (bool, error) {
		newADConfig, err := client.Management.AuthConfig.ByID(activeDirectory)
		if err != nil {
			return false, nil
		}

		if val, ok := newADConfig.Annotations[authProvCleanupAnnotationKey]; ok && val == expectedAnnotation {
			adConfig = newADConfig
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return adConfig, nil
}

// loginAsAuthUser authenticates a user with the specified auth provider and returns an authenticated client
func loginAsAuthUser(client *rancher.Client, authProvider auth.Provider, user *v3.User) (*rancher.Client, error) {
	var userEnabled = true
	user.Enabled = &userEnabled
	return client.AsAuthUser(user, authProvider)
}

// newPrincipalID constructs a principal ID string in the format required by Active Directory authentication
func newPrincipalID(authConfigID, principalType, name, userSearchBase, groupSearchBase string) string {
	var baseDN string
	if principalType == "group" {
		baseDN = groupSearchBase
	} else {
		baseDN = userSearchBase
	}
	return fmt.Sprintf("%v_%v://CN=%v,%v", authConfigID, principalType, name, baseDN)
}

// newAuthConfigWithAccessMode retrieves the current auth config and returns both the existing config and an updated version with the specified access mode
func newAuthConfigWithAccessMode(client *rancher.Client, authConfigID, accessMode string, allowedPrincipalIDs []string) (existing, updates *v3.AuthConfig, err error) {
	existing, err = client.Management.AuthConfig.ByID(authConfigID)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to retrieve auth config: %w", err)
	}

	updates = existing
	updates.AccessMode = accessMode

	if allowedPrincipalIDs != nil {
		updates.AllowedPrincipalIDs = allowedPrincipalIDs
	}

	return existing, updates, nil
}

// verifyUserLogins attempts to authenticate each user in the provided list and verifies that the login succeeds or fails as expected
func verifyUserLogins(authAdmin *rancher.Client, authProvider auth.Provider, users []User, description string, shouldSucceed bool) error {
	for _, userInfo := range users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}

		_, err := loginAsAuthUser(authAdmin, authProvider, user)

		if shouldSucceed {
			if err != nil {
				logrus.WithError(err).Errorf("✗ User [%v] failed to login: %s", userInfo.Username, description)
				return fmt.Errorf("user [%v] should be able to login (%s): %w", userInfo.Username, description, err)
			}
			logrus.Infof("✓ User [%v] login successful: %s", userInfo.Username, description)
		} else {
			if err == nil {
				logrus.Errorf("✗ User [%v] should NOT have been able to login: %s", userInfo.Username, description)
				return fmt.Errorf("user [%v] should NOT be able to login (%s)", userInfo.Username, description)
			}
			logrus.Infof("✓ User [%v] correctly denied: %s", userInfo.Username, description)
		}
	}

	return nil
}

// ensureActiveDirectoryEnabled checks if Active Directory authentication is enabled and enables it if disabled
func ensureActiveDirectoryEnabled(client *rancher.Client) error {
	adConfig, err := client.Management.AuthConfig.ByID(activeDirectory)
	if err != nil {
		return fmt.Errorf("failed to check Active Directory status: %w", err)
	}

	if !adConfig.Enabled {
		err = client.Auth.ActiveDirectory.Enable()
		if err != nil {
			return fmt.Errorf("failed to re-enable Active Directory for test: %w", err)
		}
	}

	return nil
}

// setupAuthenticatedTest creates a new test session and returns an authenticated admin client
func setupAuthenticatedTest(client *rancher.Client, session *session.Session, adminUser *v3.User) (*session.Session, *rancher.Client, error) {
	err := ensureActiveDirectoryEnabled(client)
	if err != nil {
		return nil, nil, err
	}

	subSession := session.NewSession()

	newClient, err := client.WithSession(subSession)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client with new session: %w", err)
	}

	authAdmin, err := loginAsAuthUser(newClient, auth.ActiveDirectoryAuth, adminUser)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate admin: %w", err)
	}

	return subSession, authAdmin, nil
}

// waitForNamespaceReady polls until the namespace is available within the specified timeout
func waitForNamespaceReady(client *rancher.Client, namespaceName string, timeout time.Duration) error {
	logrus.Infof("Waiting for namespace [%v] to be ready", namespaceName)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	return kwait.PollUntilContextTimeout(ctx, defaults.FiveSecondTimeout, timeout, false, func(_ context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

// getGroupPrincipalID constructs a group principal ID using the client's Active Directory configuration
func getGroupPrincipalID(client *rancher.Client, groupName string) string {
	return newPrincipalID(
		activeDirectory,
		"group",
		groupName,
		client.Auth.ActiveDirectory.Config.Users.SearchBase,
		client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)
}

// getUserPrincipalID constructs a user principal ID using the client's Active Directory configuration
func getUserPrincipalID(client *rancher.Client, username string) string {
	return newPrincipalID(
		activeDirectory,
		"user",
		username,
		client.Auth.ActiveDirectory.Config.Users.SearchBase,
		client.Auth.ActiveDirectory.Config.Groups.SearchBase,
	)
}

// updateAccessMode updates the auth config to the specified access mode with optional allowed principal IDs
func updateAccessMode(client *rancher.Client, accessMode string, allowedPrincipalIDs []string) (*v3.AuthConfig, error) {
	existing, updates, err := newAuthConfigWithAccessMode(client, activeDirectory, accessMode, allowedPrincipalIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to prepare auth config with access mode %s: %w", accessMode, err)
	}

	newAuthConfig, err := client.Auth.ActiveDirectory.Update(existing, updates)
	if err != nil {
		return nil, fmt.Errorf("failed to update auth config to access mode %s: %w", accessMode, err)
	}

	return newAuthConfig, nil
}

// setupRequiredAccessModeTest creates cluster role binding and prepares principal IDs for required access mode tests
func setupRequiredAccessModeTest(client *rancher.Client, authAdmin *rancher.Client, clusterID string, authConfig *AuthConfig) ([]string, error) {
	groupPrincipalID := getGroupPrincipalID(client, authConfig.Groups)
	_, err := rbac.CreateGroupClusterRoleTemplateBinding(authAdmin, clusterID, groupPrincipalID, rbac.ClusterMember.String())
	if err != nil {
		return nil, fmt.Errorf("failed to create cluster role binding: %w", err)
	}

	var principalIDs []string
	principalIDs = append(principalIDs, groupPrincipalID)

	for _, v := range authConfig.Users {
		userPrincipal := getUserPrincipalID(client, v.Username)
		principalIDs = append(principalIDs, userPrincipal)
		logrus.Infof("Added user principal to allowed list: %s", userPrincipal)
	}

	logrus.Infof("Total allowed principals for required mode: %v", principalIDs)
	return principalIDs, nil
}

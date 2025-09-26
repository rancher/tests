package openldap

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/auth"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	passwordSecretID                     = "cattle-global-data/openldapconfig-serviceaccountpassword"
	authProvCleanupAnnotationKey         = "management.cattle.io/auth-provider-cleanup"
	authProvCleanupAnnotationValLocked   = "rancher-locked"
	authProvCleanupAnnotationValUnlocked = "unlocked"
	ConfigurationFileKey                 = "authInput"
	openLdap                             = "openldap"
)

type User struct {
	Username string `json:"username,omitempty" yaml:"username,omitempty"`
	Password string `json:"password,omitempty" yaml:"password,omitempty"`
}

type AuthConfig struct {
	Group             string `json:"group,omitempty" yaml:"group,omitempty"`
	Users             []User `json:"users,omitempty" yaml:"users,omitempty"`
	NestedGroup       string `json:"nestedGroup,omitempty" yaml:"nestedGroup,omitempty"`
	NestedUsers       []User `json:"nestedUsers,omitempty" yaml:"nestedUsers,omitempty"`
	DoubleNestedGroup string `json:"doubleNestedGroup,omitempty" yaml:"doubleNestedGroup,omitempty"`
	DoubleNestedUsers []User `json:"doubleNestedUsers,omitempty" yaml:"doubleNestedUsers,omitempty"`
}

// waitForAuthProviderAnnotationUpdate polls the auth config until the cleanup annotation value changes
func waitForAuthProviderAnnotationUpdate(client *rancher.Client) (*v3.AuthConfig, error) {
	ldapConfig, err := client.Management.AuthConfig.ByID(openLdap)

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveHundredMillisecondTimeout, defaults.TwoMinuteTimeout, true, func(context.Context) (bool, error) {
		newLDAPConfig, err := client.Management.AuthConfig.ByID(openLdap)
		if err != nil {
			return false, nil
		}

		if ldapConfig.Annotations[authProvCleanupAnnotationKey] != newLDAPConfig.Annotations[authProvCleanupAnnotationKey] {
			ldapConfig = newLDAPConfig
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return nil, err
	}

	return ldapConfig, nil
}

// loginAsAuthUser authenticates a user with the specified auth provider and returns an authenticated client
func loginAsAuthUser(client *rancher.Client, authProvider auth.Provider, user *v3.User) (*rancher.Client, error) {
	var userEnabled = true
	user.Enabled = &userEnabled
	return client.AsAuthUser(user, authProvider)
}

// newPrincipalID constructs a principal ID string in the format required by OpenLDAP authentication for users or groups based on the provided auth config
func newPrincipalID(authConfigID, principalType, name, searchBase string) string {
	return fmt.Sprintf("%v_%v://cn=%v,ou=%vs,%v", authConfigID, principalType, name, principalType, searchBase)
}

// newAuthConfigWithAccessMode retrieves the current auth config and returns both the existing config
func newAuthConfigWithAccessMode(client *rancher.Client, authConfigID, accessMode string, allowedPrincipalIDs []string) (existing, updates *v3.AuthConfig, err error) {
	existing, err = client.Management.AuthConfig.ByID(authConfigID)

	if err != nil {
		return nil, nil, err
	}

	updates = existing
	updates.AccessMode = accessMode

	if allowedPrincipalIDs != nil {
		updates.AllowedPrincipalIDs = allowedPrincipalIDs
	}

	return existing, updates, nil
}

// verifyUserLogins attempts to authenticate each user in the provided list and verifies that the login succeeds or fails as expected
func verifyUserLogins(
	authAdmin *rancher.Client,
	authProvider auth.Provider,
	users []User,
	description string,
	shouldSucceed bool,
) error {
	for _, userInfo := range users {
		user := &v3.User{
			Username: userInfo.Username,
			Password: userInfo.Password,
		}

		logrus.Infof("Verifying user [%v] %s", userInfo.Username, description)
		_, err := loginAsAuthUser(authAdmin, authProvider, user)

		if err != nil {
			logrus.WithFields(logrus.Fields{
				"username": userInfo.Username,
				"error":    err.Error(),
			}).Errorf("User failed to login: %s", description)
		} else {
			logrus.WithField("username", userInfo.Username).Infof("User login successful: %s", description)
		}

		if shouldSucceed && err != nil {
			return fmt.Errorf("user [%v] should be able to login (%s): %w", userInfo.Username, description, err)
		}
		if !shouldSucceed && err == nil {
			return fmt.Errorf("user [%v] should NOT be able to login (%s)", userInfo.Username, description)
		}
	}

	return nil
}

// ensureOLDAPEnabled checks if OpenLDAP authentication is enabled and enables it if disabled
func ensureOLDAPEnabled(client *rancher.Client) error {
	ldapConfig, err := client.Management.AuthConfig.ByID(openLdap)
	if err != nil {
		return fmt.Errorf("failed to check OLDAP status: %w", err)
	}

	if !ldapConfig.Enabled {
		logrus.Info("OLDAP is disabled, re-enabling for test")
		err = client.Auth.OLDAP.Enable()
		if err != nil {
			return fmt.Errorf("failed to re-enable OLDAP for test: %w", err)
		}
	}

	return nil
}

// setupAuthenticatedTest creates a new test session and returns an authenticated admin client for tests that require OpenLDAP authentication
func setupAuthenticatedTest(client *rancher.Client, session *session.Session, adminUser *v3.User) (*session.Session, *rancher.Client, error) {
	err := ensureOLDAPEnabled(client)
	if err != nil {
		return nil, nil, err
	}

	subSession := session.NewSession()

	newClient, err := client.WithSession(subSession)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create client with new session: %w", err)
	}

	authAdmin, err := loginAsAuthUser(newClient, auth.OpenLDAPAuth, adminUser)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to authenticate admin: %w", err)
	}

	return subSession, authAdmin, nil
}

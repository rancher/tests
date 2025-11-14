package users

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"time"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/rbac"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	UserPasswordSecretNamespace = "cattle-local-user-passwords"
	PasswordHashAnnotation      = "cattle.io/password-hash"
	PasswordHash                = "pbkdf2sha3512"
	passwordChars               = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789!@#$%^&*()-_=+[]{}<>?"
	DummyUserName               = "dummyuser1"
	DummyPassword               = "DummyPassword1!"
)

// CreateUserWithPasswordSecret creates a user and a secret for the password using wrangler context
func CreateUserWithPasswordSecret(client *rancher.Client, passwordLength int) (*v3.User, string, error) {
	createdUser, err := CreateUser(client)
	if err != nil {
		return nil, "", err
	}

	_, password, err := CreateUserPassword(client, createdUser.Username, passwordLength)
	if err != nil {
		return nil, "", err
	}

	return createdUser, password, nil
}

// CreateUser creates a user using wrangler context
func CreateUser(client *rancher.Client) (*v3.User, error) {
	username := namegen.AppendRandomString("testuser")
	displayName := fmt.Sprintf("Test User %s", username)
	description := "Created via Public API"
	enabled := true
	mustChangePassword := false

	user := &v3.User{
		ObjectMeta: metav1.ObjectMeta{
			Name: username,
		},
		DisplayName:        displayName,
		Description:        description,
		Username:           username,
		Enabled:            &enabled,
		MustChangePassword: mustChangePassword,
		PrincipalIDs:       []string{},
	}

	_, err := client.WranglerContext.Mgmt.User().Create(user)
	if err != nil {
		return nil, fmt.Errorf("failed to create user %s: %w", username, err)
	}

	createdUser, err := WaitForUserCreation(client, username)
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for user %s to exist: %w", username, err)
	}

	return createdUser, nil
}

// CreateUserPassword creates an opaque secret for a user password and returns the password.
func CreateUserPassword(client *rancher.Client, username string, passwordLength int) (*corev1.Secret, string, error) {
	generatedPassword := generateRandomPassword(passwordLength)

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      username,
			Namespace: UserPasswordSecretNamespace,
		},
		Type: corev1.SecretTypeOpaque,
		StringData: map[string]string{
			"password": generatedPassword,
		},
	}

	createdSecret, err := client.WranglerContext.Core.Secret().Create(secret)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create secret for user %s's password: %w", username, err)
	}

	return createdSecret, generatedPassword, nil
}

func generateRandomPassword(passwordLength int) string {
	password := make([]byte, passwordLength)
	maxInt := big.NewInt(int64(len(passwordChars)))

	for i := range password {
		n, err := rand.Int(rand.Reader, maxInt)
		if err != nil {
			password[i] = 'a'
			continue
		}
		password[i] = passwordChars[n.Int64()]
	}

	return string(password)
}

// CreateUserWithRoles creates a user and assigns one or more global roles using wrangler context
func CreateUserWithRoles(client *rancher.Client, globalRoles ...string) (*v3.User, string, error) {
	createdUser, password, err := CreateUserWithPasswordSecret(client, 15)
	if err != nil {
		return nil, "", err
	}

	for _, globalRole := range globalRoles {
		_, err := rbac.CreateGlobalRoleBinding(client, createdUser, globalRole)
		if err != nil {
			return nil, "", fmt.Errorf("failed to assign global role %s to user %s: %w", globalRole, createdUser.Username, err)
		}
	}

	createdUser, err = GetUserByUsername(client, createdUser.Username)
	if err != nil {
		return nil, "", fmt.Errorf("failed to fetch user %s after assigning roles: %w", createdUser.Username, err)
	}

	return createdUser, password, nil
}

// CreateUserWithRolesAndClient creates a user with roles and returns a user-scoped client
func CreateUserWithRolesAndClient(client *rancher.Client, globalRoles ...string) (*v3.User, *rancher.Client, error) {
	user, password, err := CreateUserWithRoles(client, globalRoles...)
	if err != nil {
		return nil, nil, err
	}

	userClient, err := client.AsPublicAPIUser(user, password)
	if err != nil {
		return nil, nil, err
	}

	return user, userClient, nil
}

// GetUser retrieves a user by name using wrangler context
func GetUserByName(client *rancher.Client, name string) (*v3.User, error) {
	user, err := client.WranglerContext.Mgmt.User().Get(name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get user %s: %w", name, err)
	}
	return user, nil
}

// ListUsers retrieves all users using wrangler context
func ListUsers(client *rancher.Client) (*v3.UserList, error) {
	users, err := client.WranglerContext.Mgmt.User().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	return users, nil
}

// GetUserByUsername retrieves the user by username using wrangler context
func GetUserByUsername(client *rancher.Client, username string) (*v3.User, error) {
	users, err := client.WranglerContext.Mgmt.User().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}
	for _, u := range users.Items {
		if u.Username == username {
			return &u, nil
		}
	}

	return nil, fmt.Errorf("user with username %q not found", username)
}

// UpdateUser updates an existing user using wrangler context
func UpdateUser(client *rancher.Client, user *v3.User) (*v3.User, error) {
	updatedUser, err := client.WranglerContext.Mgmt.User().Update(user)
	if err != nil {
		return nil, fmt.Errorf("failed to update user %s: %w", user.Username, err)
	}
	return updatedUser, nil
}

// DeleteUser deletes a user by name using wrangler context
func DeleteUser(client *rancher.Client, name string) error {
	err := client.WranglerContext.Mgmt.User().Delete(name, &metav1.DeleteOptions{})
	if err != nil {
		return fmt.Errorf("failed to delete user %s: %w", name, err)
	}

	err = WaitForUserDeletion(client, name)
	if err != nil {
		return fmt.Errorf("timed out waiting for user %s to be deleted: %w", name, err)
	}

	return nil
}

// WaitForUserDeletion polls until a user with the given ID is no longer found.
func WaitForUserDeletion(client *rancher.Client, userID string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.TenSecondTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Mgmt.User().Get(userID, metav1.GetOptions{})
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return true, nil
			}
			return false, err
		}
		return false, nil
	})
}

// WaitForBackingSecretDeletion polls until the backing secret for a user is deleted
func WaitForBackingSecretDeletion(client *rancher.Client, username string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TenSecondTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.Secret().Get(UserPasswordSecretNamespace, username, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		return false, nil
	})
}

// WaitForUserCreation polls until a user with the given username exists and returns the created user
func WaitForUserCreation(client *rancher.Client, name string) (*v3.User, error) {
	var createdUser *v3.User

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TenSecondTimeout, false, func(ctx context.Context) (bool, error) {
		user, err := GetUserByName(client, name)
		if err != nil {
			if k8serrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}
		createdUser = user
		return true, nil
	})
	if err != nil {
		return nil, fmt.Errorf("timed out waiting for user %s to exist: %w", name, err)
	}

	return createdUser, nil
}

// PasswordChangeRequest updates the password for a given user using wrangler context
func PasswordChangeRequest(client *rancher.Client, userID, currentPassword string, passwordLength int) (string, error) {
	newPassword := generateRandomPassword(passwordLength)
	name := fmt.Sprintf("%s-passwd-change", userID)

	passwordChangeReq := &extapi.PasswordChangeRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: extapi.PasswordChangeRequestSpec{
			CurrentPassword: currentPassword,
			NewPassword:     newPassword,
			UserID:          userID,
		},
	}

	_, err := client.WranglerContext.Ext.PasswordChangeRequest().Create(passwordChangeReq)
	if err != nil {
		return "", fmt.Errorf("failed to change password for user %s: %w", userID, err)
	}

	return newPassword, nil
}

// CreateSelfUserRequest retrieves user ID by creating a SelfUser resource using wrangler context
func CreateSelfUserRequest(client *rancher.Client) (string, error) {
	name := namegen.AppendRandomString("selfuser")
	selfUser := &extapi.SelfUser{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
	}

	selfUserOutput, err := client.WranglerContext.Ext.SelfUser().Create(selfUser)
	if err != nil {
		return "", fmt.Errorf("failed to create SelfUser %s: %w", name, err)
	}

	userID := selfUserOutput.Status.UserID

	return userID, nil
}

// CreateGroupMembershipRefreshRequest triggers a group membership refresh for a user using wrangler context
func CreateGroupMembershipRefreshRequest(client *rancher.Client, userID string) error {
	name := namegen.AppendRandomString("group-membership-refresh")
	refreshReq := &extapi.GroupMembershipRefreshRequest{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: extapi.GroupMembershipRefreshRequestSpec{
			UserID: userID,
		},
	}

	_, err := client.WranglerContext.Ext.GroupMembershipRefreshRequest().Create(refreshReq)
	if err != nil {
		return fmt.Errorf("failed to create GroupMembershipRefreshRequest %s: %w", name, err)
	}

	return nil
}

// WaitForUserLastRefreshUpdate polls until the LastRefresh timestamp for a user is updated
func WaitForUserLastRefreshUpdate(client *rancher.Client, name string, beforeTime time.Time) (time.Time, error) {
	var afterTime time.Time

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TenSecondTimeout, true, func(ctx context.Context) (bool, error) {
		attrs, err := client.WranglerContext.Mgmt.UserAttribute().Get(name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		afterTime, err = time.Parse(time.RFC3339, attrs.LastRefresh)
		if err != nil {
			return false, err
		}

		return afterTime.After(beforeTime), nil
	})

	return afterTime, err
}

// UpdateUserPassword is an action to update a specific user's password using norman API
func UpdateUserPassword(client *rancher.Client, user *management.User, password string) error {
	setPasswordInput := management.SetPasswordInput{
		NewPassword: password,
	}

	_, err := client.Management.User.ActionSetpassword(user, &setPasswordInput)
	if err != nil {
		return fmt.Errorf("failed to update password for user %s: %w", user.Name, err)
	}

	return nil
}

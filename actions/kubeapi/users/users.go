package users

import (
	"context"
	"fmt"
	"slices"
	"time"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	DummyUserName = "dummyuser1"
	DummyPassword = "DummyPassword1!"
)

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

	createdUser, err := extuserapi.CreateUser(client, user)
	if err != nil {
		return nil, fmt.Errorf("failed to create user %s: %w", username, err)
	}

	return createdUser, nil
}

// CreateUserWithPasswordSecret creates a user and a secret for the password using wrangler context
func CreateUserWithPasswordSecret(client *rancher.Client, passwordLength int) (*v3.User, string, error) {
	createdUser, err := CreateUser(client)
	if err != nil {
		return nil, "", err
	}

	_, password, err := extuserapi.CreateUserPassword(client, createdUser.Username, passwordLength)
	if err != nil {
		return nil, "", err
	}

	return createdUser, password, nil
}

// CreateUserWithRoles creates a user and assigns one or more global roles using wrangler context
func CreateUserWithRoles(client *rancher.Client, globalRoles ...string) (*v3.User, string, error) {
	createdUser, password, err := CreateUserWithPasswordSecret(client, 15)
	if err != nil {
		return nil, "", err
	}

	for _, globalRole := range globalRoles {
		_, err := rbacapi.CreateGlobalRoleBinding(client, globalRole, createdUser.Username, "", "")
		if err != nil {
			return nil, "", fmt.Errorf("failed to assign global role %s to user %s: %w", globalRole, createdUser.Username, err)
		}
	}

	createdUser, err = extuserapi.GetUserByName(client, createdUser.Username)
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

// GetUserByUsername returns the user whose Username
func GetUserByUsername(client *rancher.Client, username string) (*v3.User, error) {
	userList, err := extuserapi.ListUsers(client)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	for i := range userList.Items {
		if userList.Items[i].Username == username {
			return &userList.Items[i], nil
		}
	}

	return nil, fmt.Errorf("user with username %q not found", username)
}

// WaitForBackingSecretDeletion polls until the backing secret for a user is deleted
func WaitForBackingSecretDeletion(client *rancher.Client, username string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TenSecondTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Core.Secret().Get(extuserapi.UserPasswordSecretNamespace, username, metav1.GetOptions{})
		if k8serrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, nil
		}
		return false, nil
	})
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

// WaitForUserPrincipalID polls until the user carries the given principal ID and returns the principal IDs it settled on
func WaitForUserPrincipalID(client *rancher.Client, name, principalID string) ([]string, error) {
	var principalIDs []string

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		user, err := extuserapi.GetUserByName(client, name)
		if err != nil {
			return false, nil
		}

		principalIDs = user.PrincipalIDs

		return slices.Contains(principalIDs, principalID), nil
	})

	if err != nil {
		return principalIDs, fmt.Errorf("timed out waiting for user %s principal IDs %v to contain %s: %w", name, principalIDs, principalID, err)
	}

	return principalIDs, nil
}

// WaitForUserByPrincipalID polls until a user carrying the given principal ID exists and returns it
func WaitForUserByPrincipalID(client *rancher.Client, principalID string) (*v3.User, error) {
	var user *v3.User

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		user, _ = findUserByPrincipalID(client, principalID)

		return user != nil, nil
	})

	if err != nil {
		return nil, fmt.Errorf("timed out waiting for a user carrying principal ID %s: %w", principalID, err)
	}

	return user, nil
}

// WaitForUserByPrincipalIDDeletion polls until no user carries the given principal ID
func WaitForUserByPrincipalIDDeletion(client *rancher.Client, principalID string) error {
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		user, err := findUserByPrincipalID(client, principalID)
		if err != nil {
			return false, nil
		}

		return user == nil, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for the user carrying principal ID %s to be removed: %w", principalID, err)
	}

	return nil
}

// WaitForUserDeletion polls until the named user no longer exists
func WaitForUserDeletion(client *rancher.Client, name string) error {
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := extuserapi.GetUserByName(client, name)
		if k8serrors.IsNotFound(err) {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for user %s to be removed: %w", name, err)
	}

	return nil
}

// ListUsersByPrincipalID returns every user carrying the given principal ID
func ListUsersByPrincipalID(client *rancher.Client, principalID string) ([]v3.User, error) {
	userList, err := extuserapi.ListUsers(client)
	if err != nil {
		return nil, fmt.Errorf("failed to list users: %w", err)
	}

	var matches []v3.User
	for i := range userList.Items {
		if slices.Contains(userList.Items[i].PrincipalIDs, principalID) {
			matches = append(matches, userList.Items[i])
		}
	}

	return matches, nil
}

func findUserByPrincipalID(client *rancher.Client, principalID string) (*v3.User, error) {
	matches, err := ListUsersByPrincipalID(client, principalID)
	if err != nil {
		return nil, err
	}

	if len(matches) == 0 {
		return nil, nil
	}

	return &matches[0], nil
}

// AddUserWithRoleToCluster creates a user based on the global role and then adds the user to cluster with provided permissions and returns a v3.User and a user-scoped client for the created user.
func AddUserWithRoleToCluster(client *rancher.Client, globalRole, role string, cluster *management.Cluster, project *v3.Project) (*v3.User, *rancher.Client, error) {
	standardUser, standardUserClient, err := CreateUserWithRolesAndClient(client, globalRole)
	if err != nil {
		return nil, nil, err
	}

	roleContext, err := rbacapi.GetRoleTemplateContext(client, role)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get context for role %s: %w", role, err)
	}

	switch roleContext {
	case rbacapi.ProjectContext:
		if project == nil {
			return nil, nil, fmt.Errorf("project is required for project-scoped role: %s", role)
		}
		_, err = rbacapi.CreateProjectRoleTemplateBinding(client, standardUser.Username, project, role)
		if err != nil {
			return nil, nil, err
		}
	case rbacapi.ClusterContext:
		if cluster == nil {
			return nil, nil, fmt.Errorf("cluster is required for cluster-scoped role: %s", role)
		}
		_, err = rbacapi.CreateClusterRoleTemplateBinding(client, cluster.ID, standardUser.Username, role)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unknown context %s for role %s", roleContext, role)
	}

	standardUserClient, err = standardUserClient.ReLogin()
	if err != nil {
		return nil, nil, err
	}

	return standardUser, standardUserClient, nil
}

package users

import (
	"fmt"

	apiv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	normanusers "github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	managementAPIGroup     = "management.cattle.io"
	usersResource          = "users"
	principalIDsField      = "principalIds"
	usernameField          = "username"
	descriptionField       = "description"
	displayNameField       = "name"
	displayNameCRDField    = "displayName"
	enabledField           = "enabled"
	unemittedPrincipalName = "neveremitted"
	hijackedUsername       = "hijacked"
	multiMatchUsername     = "multimatch"
	grantProbeDescription  = "Edited by a user holding update on users resource"
	legitimateDescription  = "Legitimate edit alongside an identity mutation"
	updatedDescription     = "Updated by admin"
	updatedDisplayName     = "Updated Display Name"
)

// newTargetUser creates a user over the norman API and returns it once its local principal has been set
func newTargetUser(client *rancher.Client) (*management.User, []string, error) {
	targetUser, err := normanusers.CreateUserWithRole(client, normanusers.UserConfig(), rbac.StandardUser.String())
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create the target user: %w", err)
	}

	principalIDs, err := userapi.WaitForUserPrincipalID(client, targetUser.ID, authactions.LocalPrincipalPrefix+targetUser.ID)
	if err != nil {
		return nil, nil, fmt.Errorf("target user %s never received its local principal: %w", targetUser.ID, err)
	}

	return targetUser, principalIDs, nil
}

// newUserWithUsersUpdateRole creates a user granted only get, list and update on users resource and returns a client authenticated as that user
func newUserWithUsersUpdateRole(client *rancher.Client) (*rancher.Client, error) {
	rules := []rbacv1.PolicyRule{
		{
			APIGroups: []string{managementAPIGroup},
			Resources: []string{usersResource},
			Verbs:     []string{"get", "list", "update"},
		},
	}

	usersUpdateRole, err := rbacapi.CreateGlobalRoleWithAllRules(client, nil, rules, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create a global role granting update on users resource: %w", err)
	}

	updaterUser, err := normanusers.CreateUserWithRole(client, normanusers.UserConfig(), rbac.StandardUser.String(), usersUpdateRole.Name)
	if err != nil {
		return nil, fmt.Errorf("failed to create a user holding update on users resource: %w", err)
	}

	updaterClient, err := client.AsUser(updaterUser)
	if err != nil {
		return nil, fmt.Errorf("failed to login as the user holding update on users resource: %w", err)
	}

	return updaterClient, nil
}

// updateUserViaNormanAPI applies the given field updates to a user over the norman API
func updateUserViaNormanAPI(client *rancher.Client, userID string, updates map[string]any) error {
	normanUser, err := client.Management.User.ByID(userID)
	if err != nil {
		return fmt.Errorf("failed to fetch user %s over the norman API: %w", userID, err)
	}

	_, err = client.Management.User.Update(normanUser, updates)

	return err
}

// plantUserCarryingPrincipal creates a user record already carrying the given principal at create time
func plantUserCarryingPrincipal(client *rancher.Client, principalID string) (*apiv3.User, error) {
	name := namegen.AppendRandomString(multiMatchUsername)

	return extuserapi.CreateUser(client, &apiv3.User{
		ObjectMeta:   metav1.ObjectMeta{Name: name},
		Username:     name,
		DisplayName:  name,
		PrincipalIDs: []string{authactions.LocalPrincipalPrefix + name, principalID},
	})
}

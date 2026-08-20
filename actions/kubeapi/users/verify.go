package users

import (
	"fmt"
	"slices"

	"github.com/rancher/shepherd/clients/rancher"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
)

// VerifyUserIdentityFieldsUnchanged re-reads the user and confirms its principal IDs, username and display name still match the expected values
func VerifyUserIdentityFieldsUnchanged(client *rancher.Client, name string, expectedPrincipalIDs []string, expectedUsername, expectedDisplayName string) error {
	user, err := extuserapi.GetUserByName(client, name)
	if err != nil {
		return fmt.Errorf("failed to get user %s: %w", name, err)
	}

	if !slices.Equal(user.PrincipalIDs, expectedPrincipalIDs) {
		return fmt.Errorf("principal IDs for user %s changed from %v to %v", name, expectedPrincipalIDs, user.PrincipalIDs)
	}

	if user.Username != expectedUsername {
		return fmt.Errorf("username for user %s changed from %q to %q", name, expectedUsername, user.Username)
	}

	if user.DisplayName != expectedDisplayName {
		return fmt.Errorf("display name for user %s changed from %q to %q", name, expectedDisplayName, user.DisplayName)
	}

	return nil
}

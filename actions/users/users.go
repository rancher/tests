package users

import (
	"fmt"
	
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
)

// UpdateUserPassword is an action to update a specific user's password
func UpdateUserPassword(client *rancher.Client, user *management.User, password string) (error) {
	setPasswordInput := management.SetPasswordInput{
		NewPassword: password,
	}

	_, err := client.Management.User.ActionSetpassword(user, &setPasswordInput)
	if err != nil {
		return fmt.Errorf("failed to update password for user %s: %w", user.Name, err)
	}

	return nil
}


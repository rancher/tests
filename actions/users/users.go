package users

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
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

// WaitForUserDeletion polls until a user with the given ID is no longer found.
func WaitForUserDeletion(client *rancher.Client, userID string) error {
	return kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
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

package useractivity

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// GetUserActivity fetches the userActivity for the corresponding session token
func GetUserActivity(client *rancher.Client, tokenName string) (*extapi.UserActivity, error) {
	userActivity, err := client.WranglerContext.Ext.UserActivity().Get(tokenName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get user activity for %s: %w", tokenName, err)
	}
	return userActivity, nil
}

// UpdateUserActivity updates the userActivity by name
func UpdateUserActivity(client *rancher.Client, userActivity *extapi.UserActivity) (*extapi.UserActivity, error) {
	userActivity, err := client.WranglerContext.Ext.UserActivity().Update(userActivity)
	if err != nil {
		return nil, fmt.Errorf("failed to update user activity")
	}
	return userActivity, nil
}

// WaitForUserActivityError is a helper function that polls for the UserActivity until a forbidden error occurs
func WaitForUserActivityError(client *rancher.Client, extSessionTokenName string) error {
	var pollError error

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.TenSecondTimeout, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		_, err := GetUserActivity(client, extSessionTokenName)
		pollError = err
		if err == nil {
			return false, nil
		}

		if k8serrors.IsForbidden(err) {
			return true, nil
		}

		return false, err
	})
	if err != nil {
		return fmt.Errorf("polling failed: %w (last error: %v)", err, pollError)
	}

	return pollError
}
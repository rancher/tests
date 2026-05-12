package secrets

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// DeleteSecret deletes a secret from a specific namespace in the given cluster using the wrangler client
func DeleteSecret(client *rancher.Client, clusterID, namespaceName, secretName string) error {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return fmt.Errorf("failed to get cluster context: %w", err)
	}

	err = clusterContext.Core.Secret().Delete(namespaceName, secretName, &metav1.DeleteOptions{})
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		_, err = clusterContext.Core.Secret().Get(namespaceName, secretName, metav1.GetOptions{})
		if errors.IsNotFound(err) {
			return true, nil
		}

		return false, err
	})
}

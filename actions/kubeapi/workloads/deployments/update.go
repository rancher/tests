package deployments

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	appv1 "k8s.io/api/apps/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpdateDeployment is a helper function to update a deployment in a cluster. If waitForDeployment is true, the function will wait for the deployment to be active
func UpdateDeployment(client *rancher.Client, clusterID, namespaceName string, deployment *appv1.Deployment, waitForDeployment bool) (*appv1.Deployment, error) {
	wranglerContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	backoff := kwait.Backoff{
		Duration: defaults.FiveSecondTimeout,
		Factor:   1,
		Jitter:   0,
		Steps:    10,
	}

	var updatedDeployment *appv1.Deployment
	// Waiting for update to succeed
	err = kwait.ExponentialBackoff(backoff, func() (finished bool, err error) {
		latestDeployment, err := wranglerContext.Apps.Deployment().Get(namespaceName, deployment.Name, metav1.GetOptions{})
		if err != nil {
			return false, err
		}

		deployment.ResourceVersion = latestDeployment.ResourceVersion

		updatedDeployment, err = wranglerContext.Apps.Deployment().Update(deployment)
		if err != nil {
			if apierrors.IsConflict(err) {
				// If there is a conflict, we need to get the latest version of the deployment and try again
				return false, nil
			}
			return false, err
		}
		return true, nil
	})
	if waitForDeployment {
		err = WaitForDeploymentActive(client, clusterID, namespaceName, deployment.Name)
		if err != nil {
			return nil, err
		}
	}

	return updatedDeployment, err
}

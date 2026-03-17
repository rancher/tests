package deployments

import (
	"github.com/rancher/shepherd/clients/rancher"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	appv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// UpdateDeployment is a helper function to update a deployment in a cluster. If waitForDeployment is true, the function will wait for the deployment to be active
func UpdateDeployment(client *rancher.Client, clusterID, namespaceName string, deployment *appv1.Deployment, waitForDeployment bool) (*appv1.Deployment, error) {
	wranglerContext, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	latestDeployment, err := wranglerContext.Apps.Deployment().Get(namespaceName, deployment.Name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	deployment.ResourceVersion = latestDeployment.ResourceVersion

	updatedDeployment, err := wranglerContext.Apps.Deployment().Update(deployment)
	if err != nil {
		return nil, err
	}

	if waitForDeployment {
		err = WaitForDeploymentActive(client, clusterID, namespaceName, deployment.Name)
		if err != nil {
			return nil, err
		}
	}

	return updatedDeployment, err
}

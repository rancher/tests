package deployments

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyDeploymentStatus is a helper function that checks the status of a deployment and verifies that it matches the expected status reason, message, and number of ready replicas
func VerifyDeploymentStatus(client *rancher.Client, clusterID, namespaceName, deploymentName, statusType, expectedStatusReason, expectedStatusMessage string, expectedReplicaCount int32) error {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return fmt.Errorf("failed to get cluster context: %w", err)
	}

	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		deployment, err := clusterContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		statusMsg, statusReason, err := GetLatestStatusMessageFromDeployment(deployment, statusType)
		if err != nil {
			return false, nil
		}

		if !strings.Contains(statusMsg, expectedStatusMessage) {
			return false, nil
		}

		if !strings.Contains(statusReason, expectedStatusReason) {
			return false, nil
		}

		if deployment.Status.ReadyReplicas != expectedReplicaCount {
			return false, nil
		}

		return true, nil
	},
	)
}

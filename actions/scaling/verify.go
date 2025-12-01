package scaling

import (
	"context"
	"testing"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	appv1 "k8s.io/api/apps/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	AutoscalerDeploymentName   = "cluster-autoscaler-clusterapi-kubernetes-cluster-autoscaler"
	autoscalerPausedAnnotation = "provisioning.cattle.io/cluster-autoscaler-paused"
)

func VerifyAutoscaler(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	require.NoError(t, err)
	require.NotNil(t, downstreamClient)

	deploymentClient := downstreamClient.SteveType(stevetypes.Deployment)

	logrus.Debug("Waiting for autoscaler deployment replicas to be available")
	var deployment *appv1.Deployment
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TwoMinuteTimeout, true, func(context.Context) (done bool, err error) {
		autoscalerDeployment, err := deploymentClient.ByID(namespaces.KubeSystem + "/" + AutoscalerDeploymentName)
		if err != nil {
			return false, nil
		}

		deployment = &appv1.Deployment{}
		err = steveV1.ConvertToK8sType(autoscalerDeployment.JSONResp, deployment)
		if *deployment.Spec.Replicas != deployment.Status.AvailableReplicas {
			return false, nil
		}

		return true, nil
	})
	require.NoError(t, err)

	if cluster.Annotations[autoscalerPausedAnnotation] == "true" {
		require.Zero(t, *deployment.Spec.Replicas)
	} else {
		require.NotZero(t, *deployment.Spec.Replicas)
	}
}

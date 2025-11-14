package scaling

import (
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/stretchr/testify/require"
	appv1 "k8s.io/api/apps/v1"
)

const (
	autoscalerDeployment       = "cluster-autoscaler-clusterapi-kubernetes-cluster-autoscaler"
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

	autoscalerDeployment, err := deploymentClient.ByID(namespaces.KubeSystem + "/" + autoscalerDeployment)
	require.NoError(t, err)

	deployment := &appv1.Deployment{}
	err = steveV1.ConvertToK8sType(autoscalerDeployment.JSONResp, deployment)
	require.NoError(t, err)
	require.Equal(t, *deployment.Spec.Replicas, deployment.Status.AvailableReplicas)

	if cluster.Annotations[autoscalerPausedAnnotation] == "true" {
		require.Zero(t, *deployment.Spec.Replicas)
	} else {
		require.NotZero(t, *deployment.Spec.Replicas)
	}
}

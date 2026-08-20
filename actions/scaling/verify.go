package scaling

import (
	"context"
	"fmt"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
	appv1 "k8s.io/api/apps/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	AutoscalerDeploymentName   = "cluster-autoscaler-clusterapi-kubernetes-cluster-autoscaler"
	autoscalerPausedAnnotation = "provisioning.cattle.io/cluster-autoscaler-paused"
)

func VerifyAutoscaler(client *rancher.Client, cluster *v1.SteveAPIObject) error {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(cluster.Status, status)
	if err != nil {
		return err
	}

	downstreamClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	if err != nil {
		return err
	}
	if downstreamClient == nil {
		return fmt.Errorf("downstream client is nil")
	}

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
	if err != nil {
		return err
	}

	if cluster.Annotations[autoscalerPausedAnnotation] == "true" {
		if 0 != *deployment.Spec.Replicas {
			return fmt.Errorf("expected 0 replicas, got %d", *deployment.Spec.Replicas)
		}
	} else {
		if 0 == *deployment.Spec.Replicas {
			return fmt.Errorf("expected non-zero replicas, got %d", *deployment.Spec.Replicas)
		}
	}

	return nil
}

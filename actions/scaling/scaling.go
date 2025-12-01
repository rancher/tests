package scaling

import (
	"context"
	"time"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func WatchAndWaitForAutoscaling(client *rancher.Client, cluster *v1.SteveAPIObject, expectedQuantity int32, timeout time.Duration) error {
	autoscalerMachinePools, err := getAutoscalerMachinePools(cluster)
	for _, autoscalerMachinePool := range autoscalerMachinePools {
		logrus.Debugf("Waiting for %s to scale from %v to %v ", autoscalerMachinePool.Name, *autoscalerMachinePool.Quantity, expectedQuantity)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 30*time.Second, timeout, true, func(context.Context) (done bool, err error) {
		cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(cluster.ID)
		if err != nil {
			return false, nil
		}

		clusterSpec := &apisV1.ClusterSpec{}
		err = steveV1.ConvertToK8sType(cluster.Spec, clusterSpec)
		if err != nil {
			return false, nil
		}

		autoscalerMachinePools, err := getAutoscalerMachinePools(cluster)
		if err != nil {
			return false, nil
		}

		for _, autoscalerMachinePool := range autoscalerMachinePools {
			if *autoscalerMachinePool.Quantity != expectedQuantity {
				return false, err
			}
		}

		return true, nil
	})

	return err
}

func getAutoscalerMachinePools(cluster *v1.SteveAPIObject) ([]apisV1.RKEMachinePool, error) {
	clusterSpec := &apisV1.ClusterSpec{}
	err := steveV1.ConvertToK8sType(cluster.Spec, clusterSpec)
	if err != nil {
		return nil, err
	}

	var autoscalerPools []apisV1.RKEMachinePool
	for _, machinePool := range clusterSpec.RKEConfig.MachinePools {
		if machinePool.AutoscalingMinSize != nil && machinePool.AutoscalingMaxSize != nil && machinePool.WorkerRole {
			autoscalerPools = append(autoscalerPools, machinePool)
		}
	}

	return autoscalerPools, err
}

func UpdateAutoscalerState(client *rancher.Client, cluster *v1.SteveAPIObject, pause bool) error {
	apiCluster, cluster, err := clusters.GetProvisioningClusterByName(client, cluster.Name, namespaces.FleetDefault)
	if err != nil {
		return err
	}

	if pause {
		apiCluster.Annotations[autoscalerPausedAnnotation] = "true"
	} else {
		delete(apiCluster.Annotations, autoscalerPausedAnnotation)
	}

	_, err = client.Steve.SteveType(stevetypes.Provisioning).Update(cluster, apiCluster)
	if err != nil {
		return err
	}

	return err
}

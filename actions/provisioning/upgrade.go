package provisioning

import (
	"context"
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/bundledclusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpgradeClusterK8sVersion upgrades the cluster to the specified version
func UpgradeClusterK8sVersion(client *rancher.Client, clusterName *string, upgradeVersion *string) (*bundledclusters.BundledCluster, error) {
	clusterMeta, err := clusters.NewClusterMeta(client, *clusterName)
	if err != nil {
		return nil, err
	}
	if clusterMeta == nil {
		return nil, fmt.Errorf("cluster %s not found", *clusterName)
	}

	initCluster, err := bundledclusters.NewWithClusterMeta(clusterMeta)
	if err != nil {
		return nil, err
	}

	cluster, err := initCluster.Get(client)
	if err != nil {
		return nil, err
	}

	updatedCluster, err := cluster.UpdateKubernetesVersion(client, upgradeVersion)
	if err != nil {
		return nil, err
	}

	err = clusters.WaitClusterToBeUpgraded(client, clusterMeta.ID)
	if err != nil {
		return nil, err
	}
	return updatedCluster, nil
}

// WaitClusterToBeUpgradedWithRetry wraps WaitClusterToBeUpgraded with retry logic to handle transient watch failures
func WaitClusterToBeUpgradedWithRetry(client *rancher.Client, clusterID string) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		clusterResp, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
		if err != nil {
			return false, nil
		}

		state := clusterResp.ObjectMeta.State.Name
		if state == updating || state == upgrading {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timeout waiting for cluster to enter update state: %w", err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		clusterResp, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
		if err != nil {
			return false, nil
		}

		state := clusterResp.ObjectMeta.State.Name
		if state == active {
			logrus.Debugf("Cluster %s update complete, state: active", clusterID)
			return true, nil
		}

		if clusterResp.ObjectMeta.State.Error {
			return false, fmt.Errorf("cluster %s encountered error during upgrade: %s", clusterID, clusterResp.ObjectMeta.State.Message)
		}

		return false, nil
	})

	return err
}

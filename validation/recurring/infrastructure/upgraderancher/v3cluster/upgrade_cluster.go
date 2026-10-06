package localcluster

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/defaults"
	actiondefaults "github.com/rancher/tests/actions/config/defaults"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	active = "active"
)

// UpgradeV3Cluster is a function that will upgrade the local or imported cluster.
func UpgradeV3Cluster(client *rancher.Client, clusterName string) error {
	clusterObj, err := extClusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	clusterResp, err := client.Management.Cluster.ByID(clusterObj)
	if err != nil {
		return err
	}

	initialCluster, err := client.Management.Cluster.ByID(clusterResp.ID)
	if err != nil {
		return err
	}

	initialVersion := initialCluster.Version.GitVersion
	if initialVersion == "" {
		return fmt.Errorf("initial cluster version is empty")
	}

	var clusterType string
	if strings.Contains(initialVersion, actiondefaults.K3S) {
		clusterType = actiondefaults.K3S
	} else if strings.Contains(initialVersion, actiondefaults.RKE2) {
		clusterType = actiondefaults.RKE2
	}

	version, err := kubernetesversions.Default(client, clusterType, nil)
	if err != nil {
		return err
	}

	var updatedCluster *management.Cluster
	if clusterType == actiondefaults.K3S {
		updatedCluster = &management.Cluster{
			K3sConfig: &management.K3sConfig{
				Version: version[0],
			},
			Name: clusterResp.Name,
		}
	} else if clusterType == actiondefaults.RKE2 {
		updatedCluster = &management.Cluster{
			Rke2Config: &management.Rke2Config{
				Version: version[0],
			},
			Name: clusterResp.Name,
		}
	}

	updatedClusterResp, err := client.Management.Cluster.Update(clusterResp, updatedCluster)
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		client, err = client.ReLogin()
		if err != nil {
			return false, err
		}

		clusterResp, err := client.Management.Cluster.ByID(updatedClusterResp.ID)
		if err != nil {
			return false, err
		}

		if clusterResp.State == active {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return err
	}

	if clusterType == actiondefaults.K3S {
		logrus.Infof("Cluster has been upgraded to: %s", updatedClusterResp.K3sConfig.Version)
	} else if clusterType == actiondefaults.RKE2 {
		logrus.Infof("Cluster has been upgraded to: %s", updatedClusterResp.Rke2Config.Version)
	}

	return nil
}

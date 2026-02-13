package upgrade

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/bundledclusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/upgradeinput"

	kcluster "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// UpgradeLocalCluster is a function to upgrade a local cluster.
func UpgradeLocalCluster(u *suite.Suite, client *rancher.Client, testConfig *clusters.ClusterConfig, cluster upgradeinput.Cluster) {
	clusterMeta, err := extensionscluster.NewClusterMeta(client, cluster.Name)
	require.NoError(u.T(), err)

	initCluster, err := bundledclusters.NewWithClusterMeta(clusterMeta)
	require.NoError(u.T(), err)

	initClusterResp, err := initCluster.Get(client)
	require.NoError(u.T(), err)

	preUpgradeCluster, err := client.Management.Cluster.ByID(clusterMeta.ID)
	require.NoError(u.T(), err)

	if strings.Contains(preUpgradeCluster.Version.GitVersion, testConfig.KubernetesVersion) {
		u.T().Skipf("Skipping test: Kubernetes version %s already upgraded", testConfig.KubernetesVersion)
	}

	logrus.Infof("Upgrading local cluster to: %s", testConfig.KubernetesVersion)
	updatedCluster, err := initClusterResp.UpdateKubernetesVersion(client, &testConfig.KubernetesVersion)
	require.NoError(u.T(), err)

	err = waitForLocalClusterUpgrade(client, clusterMeta.ID)
	require.NoError(u.T(), err)

	upgradedCluster, err := client.Management.Cluster.ByID(updatedCluster.Meta.ID)
	require.NoError(u.T(), err)
	require.Contains(u.T(), testConfig.KubernetesVersion, upgradedCluster.Version.GitVersion)

	logrus.Infof("Local cluster has been upgraded to: %s", upgradedCluster.Version.GitVersion)
}

// UpgradeCluster is a function to upgrade the kubernetes version of a downstream cluster.
func UpgradeCluster(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject, kubernetesVersion string) (*v1.SteveAPIObject, error) {
	clusterSpec := &provv1.ClusterSpec{}
	err := v1.ConvertToK8sType(cluster.Spec, clusterSpec)
	if err != nil {
		return nil, err
	}

	clusterSpec.KubernetesVersion = kubernetesVersion
	cluster.Spec = clusterSpec

	updatedClusterObj := new(provv1.Cluster)
	err = v1.ConvertToK8sType(cluster, &updatedClusterObj)
	require.NoError(t, err)

	updatedCluster, err := extensionscluster.UpdateK3SRKE2Cluster(client, cluster, updatedClusterObj)
	require.NoError(t, err)

	return updatedCluster, nil
}

// waitForLocalClusterUpgrade is a function to wait for the local cluster to upgrade.
func waitForLocalClusterUpgrade(client *rancher.Client, clusterName string) error {

	client, err := client.ReLogin()
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 2*time.Second, defaults.FiveSecondTimeout, true, func(ctx context.Context) (done bool, err error) {
		isUpgrading, err := client.Management.Cluster.ByID(clusterName)
		if err != nil {
			return false, err
		}

		return isUpgrading.State == "upgrading" && isUpgrading.Transitioning == "yes", nil
	})
	if err != nil {
		return err
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 2*time.Second, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		isConnected, err := client.IsConnected()
		if err != nil {
			return false, nil
		}

		if isConnected {
			ready, err := kcluster.IsClusterActive(client, clusterName)
			if err != nil {
				return false, nil
			}

			return ready, nil
		}

		return false, nil
	})

	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), 2*time.Second, defaults.FiveSecondTimeout, true, func(ctx context.Context) (done bool, err error) {
		isConnected, err := client.IsConnected()
		if err != nil {
			return false, err
		}
		return isConnected, nil
	})
}

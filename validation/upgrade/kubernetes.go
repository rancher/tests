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
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/bundledclusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/upgradeinput"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"

	kcluster "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// LocalCluster is a function to upgrade a local cluster.
func LocalCluster(u *suite.Suite, client *rancher.Client, testConfig *clusters.ClusterConfig, cluster upgradeinput.Cluster) {
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

// DownstreamCluster is a function to upgrade a downstream cluster.
func DownstreamCluster(u *suite.Suite, testName string, client *rancher.Client, clusterName string, testConfig *clusters.ClusterConfig,
	clusterID, versionToUpgrade string, isRKE1 bool) {
	if isRKE1 {
		upgradedCluster, err := upgradeRKE1Cluster(u.T(), client, clusterID, testConfig)
		require.NoError(u.T(), err)

		clusterResp, err := extensionscluster.GetClusterIDByName(client, upgradedCluster.Name)
		require.NoError(u.T(), err)

		upgradedRKE1Cluster, err := client.Management.Cluster.ByID(clusterResp)
		require.NoError(u.T(), err)

		provisioning.VerifyRKE1Cluster(u.T(), client, testConfig, upgradedRKE1Cluster)
	} else {
		upgradedCluster, err := upgradeRKE2K3SCluster(u.T(), client, clusterID, testConfig)
		require.NoError(u.T(), err)

		logrus.Infof("Verifying the cluster is ready (%s)", upgradedCluster.Name)
		err = provisioning.VerifyClusterReady(client, upgradedCluster)
		require.NoError(u.T(), err)

		logrus.Infof("Verifying cluster deployments (%s)", upgradedCluster.Name)
		err = deployment.VerifyClusterDeployments(client, upgradedCluster)
		require.NoError(u.T(), err)

		logrus.Infof("Verifying cluster pods (%s)", upgradedCluster.Name)
		err = pods.VerifyClusterPods(client, upgradedCluster)
		require.NoError(u.T(), err)
	}
}

// upgradeRKE1Cluster is a function to upgrade a downstream RKE1 cluster.
func upgradeRKE1Cluster(t *testing.T, client *rancher.Client, clusterID string, clustersConfig *clusters.ClusterConfig) (*management.Cluster, error) {
	clusterResp, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	updatedCluster := clusters.UpdateRKE1ClusterConfig(clusterResp.Name, client, clustersConfig)

	updatedClusterResp, err := extensionscluster.UpdateRKE1Cluster(client, clusterResp, updatedCluster)
	require.NoError(t, err)

	upgradedCluster, err := client.Management.Cluster.ByID(updatedClusterResp.ID)
	require.NoError(t, err)
	require.Equal(t, clustersConfig.KubernetesVersion, upgradedCluster.RancherKubernetesEngineConfig.Version)

	logrus.Infof("Cluster has been upgraded to: %s", upgradedCluster.RancherKubernetesEngineConfig.Version)

	return updatedClusterResp, nil
}

// upgradeRKE2K3SCluster is a function to upgrade a downstream RKE2 or K3S cluster.
func upgradeRKE2K3SCluster(t *testing.T, client *rancher.Client, clusterID string, clustersConfig *clusters.ClusterConfig) (*v1.SteveAPIObject, error) {
	clusterResp, err := client.Steve.SteveType(extensionscluster.ProvisioningSteveResourceType).ByID(clusterID)
	require.NoError(t, err)

	updatedCluster := clusters.UpdateK3SRKE2ClusterConfig(clusterResp, clustersConfig)

	updatedClusterObj := new(provv1.Cluster)
	err = v1.ConvertToK8sType(updatedCluster, &updatedClusterObj)
	require.NoError(t, err)

	updatedClusterResp, err := extensionscluster.UpdateK3SRKE2Cluster(client, updatedCluster, updatedClusterObj)
	require.NoError(t, err)

	updatedClusterSpec := &provv1.ClusterSpec{}
	err = v1.ConvertToK8sType(updatedClusterResp.Spec, updatedClusterSpec)
	require.NoError(t, err)
	require.Equal(t, clustersConfig.KubernetesVersion, updatedClusterSpec.KubernetesVersion)

	logrus.Infof("Cluster has been upgraded to: %s", updatedClusterSpec.KubernetesVersion)

	return updatedClusterResp, nil
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

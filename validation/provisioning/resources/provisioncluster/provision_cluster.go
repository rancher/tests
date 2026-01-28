package provisioncluster

import (
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/stretchr/testify/require"
)

// ProvisionRKE2K3SCluster is a helper function that provisions an RKE2/K3s cluster with specified machine pools and node roles.
func ProvisionRKE2K3SCluster(t *testing.T, client *rancher.Client, clusterType string, provider provisioning.Provider, clusterConfig clusters.ClusterConfig, machineConfigs machinepools.MachineConfigs, ec2Configs *ec2.AWSEC2Configs,
	highestVersion, isCustomCluster bool) (*v1.SteveAPIObject, error) {
	var clusterObject *v1.SteveAPIObject
	var err error

	if clusterConfig.KubernetesVersion == "" {
		if highestVersion {
			version, err := kubernetesversions.Default(client, clusterType, nil)
			require.NoError(t, err)

			clusterConfig.KubernetesVersion = version[0]
		} else if !highestVersion && clusterType == extClusters.RKE2ClusterType.String() {
			versions, err := kubernetesversions.ListRKE2AllVersions(client)
			require.NoError(t, err)

			clusterConfig.KubernetesVersion = versions[1]
		} else if !highestVersion && clusterType == extClusters.K3SClusterType.String() {
			versions, err := kubernetesversions.ListK3SAllVersions(client)
			require.NoError(t, err)

			clusterConfig.KubernetesVersion = versions[1]
		}
	}

	if isCustomCluster {
		externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

		clusterObject, err = provisioning.CreateProvisioningCustomCluster(client, &externalNodeProvider, &clusterConfig, ec2Configs)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(client, clusterObject)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(client, clusterObject)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(client, clusterObject)
		require.NoError(t, err)
	} else {
		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))

		clusterObject, err = provisioning.CreateProvisioningCluster(client, provider, credentialSpec, &clusterConfig, machineConfigs, nil)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(client, clusterObject)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(client, clusterObject)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(client, clusterObject)
		require.NoError(t, err)
	}

	return clusterObject, nil
}

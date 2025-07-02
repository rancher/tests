package provisioncluster

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/rke1/componentchecks"
	"github.com/stretchr/testify/require"
)

// ProvisionRKE1Cluster is a helper function that provisions an RKE1 cluster with specified machine pools and node roles.
func ProvisionRKE1Cluster(t *testing.T, client *rancher.Client, provisioningConfig *provisioninginput.Config, highestVersion,
	isCustomCluster bool) (string, error) {
	var clusterObject *management.Cluster
	var nodes []*nodes.Node
	var err error

	if provisioningConfig.RKE1KubernetesVersions == nil {
		if highestVersion {
			kubernetesVersion, err := kubernetesversions.Default(client, extClusters.RKE1ClusterType.String(), nil)
			require.NoError(t, err)

			provisioningConfig.RKE1KubernetesVersions = kubernetesVersion
		} else {
			versions, err := kubernetesversions.ListRKE1AllVersions(client)
			require.NoError(t, err)

			provisioningConfig.RKE1KubernetesVersions = versions[1:2]
		}
	}

	for _, providerName := range provisioningConfig.Providers {
		for _, nodeProviderName := range provisioningConfig.NodeProviders {
			for _, kubeVersion := range provisioningConfig.RKE1KubernetesVersions {
				for _, cni := range provisioningConfig.CNIs {
					clusterConfig := clusters.ConvertConfigToClusterConfig(provisioningConfig)
					clusterConfig.Provider = providerName
					clusterConfig.KubernetesVersion = kubeVersion
					clusterConfig.NodeProvider = nodeProviderName
					clusterConfig.CNI = cni

					if isCustomCluster {
						externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

						clusterObject, nodes, err = provisioning.CreateProvisioningRKE1CustomCluster(client, &externalNodeProvider, clusterConfig)
						require.NoError(t, err)

						provisioning.VerifyRKE1Cluster(t, client, clusterConfig, clusterObject)
						etcdVersion, err := componentchecks.CheckETCDVersion(client, nodes, clusterObject.ID)
						require.NoError(t, err)
						require.NotEmpty(t, etcdVersion)
					} else {
						rke1NodeProvider := provisioning.CreateRKE1Provider(clusterConfig.Provider)
						nodeTemplate, err := rke1NodeProvider.NodeTemplateFunc(client)
						require.NoError(t, err)

						clusterObject, err = provisioning.CreateProvisioningRKE1Cluster(client, rke1NodeProvider, clusterConfig, nodeTemplate)
						require.NoError(t, err)

						provisioning.VerifyRKE1Cluster(t, client, clusterConfig, clusterObject)
					}
				}
			}
		}
	}

	return clusterObject.ID, nil
}

package globalrolesv2

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"

	"github.com/rancher/shepherd/pkg/config"

	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
)

func CreateDownstreamCluster(client *rancher.Client, clusterType string) (*management.Cluster, *v1.SteveAPIObject, *clusters.ClusterConfig, error) {
	provisioningConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, provisioningConfig)
	nodeProviders := provisioningConfig.NodeProviders[0]
	externalNodeProvider := provisioning.ExternalNodeProviderSetup(nodeProviders)
	testClusterConfig := clusters.ConvertConfigToClusterConfig(provisioningConfig)
	testClusterConfig.CNI = provisioningConfig.CNIs[0]

	var clusterObject *management.Cluster
	var steveObject *v1.SteveAPIObject
	var err error

	switch clusterType {
	case "RKE1":
		nodeAndRoles := []provisioninginput.NodePools{
			provisioninginput.AllRolesNodePool,
		}
		testClusterConfig.NodePools = nodeAndRoles
		testClusterConfig.KubernetesVersion = provisioningConfig.RKE1KubernetesVersions[0]
		clusterObject, _, err = provisioning.CreateProvisioningRKE1CustomCluster(client, &externalNodeProvider, testClusterConfig)
	case "RKE2":
		nodeAndRoles := []provisioninginput.MachinePools{
			provisioninginput.AllRolesMachinePool,
		}
		testClusterConfig.MachinePools = nodeAndRoles
		testClusterConfig.KubernetesVersion = provisioningConfig.RKE2KubernetesVersions[0]
		steveObject, err = provisioning.CreateProvisioningCustomCluster(client, &externalNodeProvider, testClusterConfig)
	default:
		return nil, nil, nil, fmt.Errorf("unsupported cluster type: %s", clusterType)
	}

	if err != nil {
		return nil, nil, nil, err
	}

	return clusterObject, steveObject, testClusterConfig, nil
}

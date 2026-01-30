package rke1

import (
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/tests/actions/provisioning"
	rke1 "github.com/rancher/tests/actions/rke1/nodepools"
	"github.com/stretchr/testify/require"
)

// ScalingRKE1NodePools scales the node pools of an RKE1 cluster.
func ScalingRKE1NodePools(t *testing.T, client *rancher.Client, clusterID string, nodeRoles rke1.NodeRoles) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	node, err := rke1.MatchRKE1NodeRoles(client, cluster, nodeRoles)
	require.NoError(t, err)

	_, err = rke1.ScaleNodePoolNodes(client, cluster, node, nodeRoles)
	require.NoError(t, err)

	nodeRoles.Quantity = -nodeRoles.Quantity
	_, err = rke1.ScaleNodePoolNodes(client, cluster, node, nodeRoles)
	require.NoError(t, err)
}

// ScalingRKE1CustomClusterPools scales the node pools of an RKE1 custom cluster.
func ScalingRKE1CustomClusterPools(t *testing.T, client *rancher.Client, clusterID string, nodeProvider string, nodeRoles rke1.NodeRoles) {
	rolesPerNode := []string{}
	quantityPerPool := []int32{}
	rolesPerPool := []string{}

	for _, pool := range []rke1.NodeRoles{nodeRoles} {
		var finalRoleCommand string

		if pool.ControlPlane {
			finalRoleCommand += " --controlplane"
		}

		if pool.Etcd {
			finalRoleCommand += " --etcd"
		}

		if pool.Worker {
			finalRoleCommand += " --worker"
		}

		quantityPerPool = append(quantityPerPool, int32(pool.Quantity))
		rolesPerPool = append(rolesPerPool, finalRoleCommand)

		for i := int64(0); i < pool.Quantity; i++ {
			rolesPerNode = append(rolesPerNode, finalRoleCommand)
		}
	}

	var externalNodeProvider provisioning.ExternalNodeProvider
	externalNodeProvider = provisioning.ExternalNodeProviderSetup(nodeProvider)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	config.LoadConfig(ec2.ConfigurationFileKey, awsEC2Configs)

	nodes, err := externalNodeProvider.NodeCreationFunc(client, rolesPerPool, quantityPerPool, awsEC2Configs, false)
	require.NoError(t, err)

	cluster, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	err = provisioning.AddRKE1CustomClusterNodes(client, cluster, nodes, rolesPerNode)
	require.NoError(t, err)

	err = provisioning.DeleteRKE1CustomClusterNodes(client, cluster, nodes)
	require.NoError(t, err)

	err = externalNodeProvider.NodeDeletionFunc(client, nodes)
	require.NoError(t, err)
}

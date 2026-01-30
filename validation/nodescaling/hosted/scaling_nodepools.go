package hosted

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters/aks"
	"github.com/rancher/shepherd/extensions/clusters/eks"
	"github.com/rancher/shepherd/extensions/clusters/gke"
	"github.com/stretchr/testify/require"
)

// ScalingAKSNodePools scales the node pools of an AKS cluster.
func ScalingAKSNodePools(t *testing.T, client *rancher.Client, clusterID string, nodePool *aks.NodePool) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	clusterResp, err := aks.ScalingAKSNodePoolsNodes(client, cluster, nodePool)
	require.NoError(t, err)

	*nodePool.NodeCount = -*nodePool.NodeCount

	_, err = aks.ScalingAKSNodePoolsNodes(client, clusterResp, nodePool)
	require.NoError(t, err)
}

// ScalingEKSNodePools scales the node pools of an EKS cluster.
func ScalingEKSNodePools(t *testing.T, client *rancher.Client, clusterID string, nodePool *eks.NodeGroupConfig) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	clusterResp, err := eks.ScalingEKSNodePoolsNodes(client, cluster, nodePool)
	require.NoError(t, err)

	*nodePool.DesiredSize = -*nodePool.DesiredSize

	_, err = eks.ScalingEKSNodePoolsNodes(client, clusterResp, nodePool)
	require.NoError(t, err)
}

// ScalingGKENodePools scales the node pools of a GKE cluster.
func ScalingGKENodePools(t *testing.T, client *rancher.Client, clusterID string, nodePool *gke.NodePool) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	clusterResp, err := gke.ScalingGKENodePoolsNodes(client, cluster, nodePool)
	require.NoError(t, err)

	*nodePool.InitialNodeCount = -*nodePool.InitialNodeCount

	_, err = gke.ScalingGKENodePoolsNodes(client, clusterResp, nodePool)
	require.NoError(t, err)
}

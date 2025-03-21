package clusters

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/wrangler"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
)

// GetClusterWranglerContext returns the context for the cluster
func GetClusterWranglerContext(client *rancher.Client, clusterID string) (*wrangler.Context, error) {
	if clusterID == rbacapi.LocalCluster {
		return client.WranglerContext, nil
	}

	return client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
}

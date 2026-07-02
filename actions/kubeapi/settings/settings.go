package settings

import (
	"github.com/rancher/shepherd/clients/rancher"
	
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
)

// GetGlobalSettingNamesForCluster lists global setting names using a cluster-scoped wrangler
// context so non-admin users can read them via the downstream cluster proxy.
func GetGlobalSettingNamesForCluster(client *rancher.Client, clusterID string) ([]string, error) {
	settingsClient := client

	if clusterID != extclusterapi.LocalCluster {
		clusterContext, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if err != nil {
			return nil, err
		}

		scopedClient := *client
		scopedClient.WranglerContext = clusterContext
		settingsClient = &scopedClient
	}

	return extsettingsapi.GetGlobalSettingNames(settingsClient)
}

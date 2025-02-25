package settings

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/wrangler"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GetGlobalSettingNames is a helper function to fetch a list of global setting names
func GetGlobalSettingNames(client *rancher.Client, clusterID string) ([]string, error) {
	var ctx *wrangler.Context
	var err error

	if clusterID != rbacapi.LocalCluster {
		ctx, err = client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if err != nil {
			return nil, fmt.Errorf("failed to get downstream context: %w", err)
		}
	} else {
		ctx = client.WranglerContext
	}

	settings, err := ctx.Mgmt.Setting().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	globalSettings := []string{}
	for _, gs := range settings.Items {
		globalSettings = append(globalSettings, gs.Name)
	}

	return globalSettings, nil
}

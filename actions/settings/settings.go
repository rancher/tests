package settings

import (
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/settings"
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

// GetRancherSetting fetches the value of a Rancher setting given its key
func GetRancherSetting(client *rancher.Client, key string) (string, error) {
	steveClient := client.Steve

	settingResp, err := steveClient.SteveType(settings.ManagementSetting).ByID(key)
	if err != nil {
		return "", fmt.Errorf("failed to get setting %s: %w", key, err)
	}

	setting := &v3.Setting{}
	err = v1.ConvertToK8sType(settingResp.JSONResp, setting)
	if err != nil {
		return "", fmt.Errorf("failed to convert setting %s: %w", key, err)
	}

	return setting.Value, nil
}

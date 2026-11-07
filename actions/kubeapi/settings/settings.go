package settings

import (
	"fmt"
	"strconv"

	"github.com/rancher/shepherd/clients/rancher"

	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
)

const (
	AuthTokenMaxTTLMinutes      = "auth-token-max-ttl-minutes"
	AuthTokenDefaultTTLMinutes  = "auth-token-default-ttl-minutes"
	KubeconfigMaxTTLMinutes     = "kubeconfig-max-token-ttl-minutes"
	KubeconfigDefaultTTLMinutes = "kubeconfig-default-token-ttl-minutes"
	AuthUserSessionTTLMinutes   = "auth-user-session-ttl-minutes"
	MillisecondsPerMinute       = int64(60000)
	SecondsPerMinute            = int64(60)
	MillisecondsPerSecond       = int64(1000)
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

// GetGlobalSettingDefaultMinutes returns the installation default of a minutes-based global setting, as an int64.
func GetGlobalSettingDefaultMinutes(client *rancher.Client, settingName string) (int64, error) {
	defaultValue, err := extsettingsapi.GetGlobalSettingDefaultValue(client, settingName)
	if err != nil {
		return 0, err
	}

	minutes, err := strconv.ParseInt(defaultValue, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse default value %q for setting %s: %w", defaultValue, settingName, err)
	}

	return minutes, nil
}

// ResetGlobalSettings resets each of the given global settings to its installation default value.
func ResetGlobalSettings(client *rancher.Client, settingNames []string) error {
	for _, settingName := range settingNames {
		if err := extsettingsapi.ResetGlobalSettingToDefaultValue(client, settingName); err != nil {
			return fmt.Errorf("failed to reset setting %s: %w", settingName, err)
		}
	}
	return nil
}

// ResetTokenTTLSettingsToDefault resets each of the given TTL settings to empty string, so Rancher falls back to the setting's installation default.
func ResetTokenTTLSettingsToDefault(client *rancher.Client, settingNames []string) error {
	for _, settingName := range settingNames {
		err := extsettingsapi.UpdateGlobalSetting(client, settingName, "")
		if err != nil {
			return fmt.Errorf("failed to reset setting %s: %w", settingName, err)
		}
	}

	return nil
}

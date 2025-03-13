package settings

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/settings"
	"github.com/rancher/shepherd/pkg/wrangler"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	AutoscalerChartRepo                  = "cluster-autoscaler-chart-repository"
	AutoscalerImage                      = "cluster-autoscaler-image"
	AuthUserSessionIdleTTlMinutesSetting = "auth-user-session-idle-ttl-minutes"
	AuthTokenMaxTTLMinutesSetting        = "auth-token-max-ttl-minutes"
	AuthTokenMaxTTLMinutes               = "auth-token-max-ttl-minutes"
	KubeconfigDefaultTTLMinutes          = "kubeconfig-default-token-ttl-minutes"
	UserPasswordMinLength                = "password-min-length"
)

// GetGlobalSettingNames is a helper function to fetch a list of global setting names
func GetGlobalSettingNames(client *rancher.Client, clusterID string) ([]string, error) {
	var ctx *wrangler.Context
	var err error

	if clusterID != clusterapi.LocalCluster {
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

// SetGlobalSetting is a helper function to set the value of a Rancher global setting given its ID
func SetGlobalSetting(client *rancher.Client, settingID, value string) error {
	setting, err := client.Steve.SteveType(settings.ManagementSetting).ByID(settingID)
	if err != nil {
		return err
	}

	_, err = settings.UpdateGlobalSettings(client.Steve, setting, value)

	return err
}

// ResetGlobalSettingToDefaultValue is a helper function to reset a global setting by name to it's default value
func ResetGlobalSettingToDefaultValue(client *rancher.Client, settingName string) error {
	setting, err := client.WranglerContext.Mgmt.Setting().Get(settingName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	setting.Value = setting.Default

	_, err = client.WranglerContext.Mgmt.Setting().Update(setting)
	if err != nil {
		return err
	}

	updatedSetting, err := client.WranglerContext.Mgmt.Setting().Get(settingName, metav1.GetOptions{})
	if err != nil {
		return err
	}

	if updatedSetting.Value != updatedSetting.Default {
		return fmt.Errorf("failed to reset setting %q to default value; got: %s, expected: %s",
			settingName, updatedSetting.Value, updatedSetting.Default)
	}

	return nil
}

// GetGlobalSettingDefaultValue is a helper function to retrieve the default value of a Rancher global setting given its ID
func GetGlobalSettingDefaultValue(client *rancher.Client, settingName string) (string, error) {
	setting, err := client.WranglerContext.Mgmt.Setting().Get(settingName, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("failed to get setting %s: %w", settingName, err)
	}

	return setting.Default, nil
}

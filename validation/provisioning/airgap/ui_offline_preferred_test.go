//go:build validation || (recurring && airgap) || airgap

package airgap

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/networking"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	uiOfflinePreferred = "ui-offline-preferred"
)

func TestUIOfflinePreferred(t *testing.T) {
	r := airgapSetup(t, defaults.K3S)

	defaultSetting, err := r.client.Management.Setting.ByID(uiOfflinePreferred)
	require.NoError(t, err)

	tests := []struct {
		name    string
		client  *rancher.Client
		setting string
	}{
		{"UI_Offline_Preferred_Dynamic", r.client, "dynamic"},
		{"UI_Offline_Preferred_Local", r.client, "true"},
		{"UI_Offline_Preferred_Remote", r.client, "false"},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			if r.tunnel != nil {
				r.tunnel.StopBastionSSHTunnel()
			}
		})

		t.Run(tt.name, func(t *testing.T) {
			setting, err := tt.client.Management.Setting.ByID(uiOfflinePreferred)
			require.NoError(t, err)

			setting.Value = tt.setting

			updatedSetting, err := tt.client.Management.Setting.Update(defaultSetting, setting)
			require.NoError(t, err)

			networking.GetPageStatus(r.rancherConfig, updatedSetting.Value)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

package monitoring

import (
	"testing"

	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
)

func TestMonitoringTestConfigApplyDefaults(t *testing.T) {
	tests := []struct {
		name     string
		config   MonitoringTestConfig
		wantImg  string
		wantPref []string
		wantSkip bool
	}{
		{
			name:     "empty config gets pinned image and default preference",
			config:   MonitoringTestConfig{},
			wantImg:  "traefik:v3.7.12",
			wantPref: []string{NodeAddressTypeExternal, NodeAddressTypeInternal},
			wantSkip: false,
		},
		{
			name:     "image override is kept and preference is defaulted",
			config:   MonitoringTestConfig{WebhookReceiverImage: "mirror.example.com/traefik:v3.7.12"},
			wantImg:  "mirror.example.com/traefik:v3.7.12",
			wantPref: []string{NodeAddressTypeExternal, NodeAddressTypeInternal},
			wantSkip: false,
		},
		{
			name:     "skip flag is preserved through defaults",
			config:   MonitoringTestConfig{SkipWebhookReceiver: true},
			wantImg:  "traefik:v3.7.12",
			wantPref: []string{NodeAddressTypeExternal, NodeAddressTypeInternal},
			wantSkip: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.config.ApplyDefaults()
			require.Equal(t, tt.wantImg, tt.config.WebhookReceiverImage)
			require.Equal(t, tt.wantPref, tt.config.NodeAddressPreference)
			require.Equal(t, tt.wantSkip, tt.config.SkipWebhookReceiver)
			require.NoError(t, tt.config.Validate())
		})
	}
}

func TestMonitoringTestConfigValidate(t *testing.T) {
	tests := []struct {
		name   string
		pref   []string
		wantEr bool
	}{
		{
			name:   "external and internal are valid",
			pref:   []string{NodeAddressTypeExternal, NodeAddressTypeInternal},
			wantEr: false,
		},
		{
			name:   "hostname is rejected",
			pref:   []string{"Hostname"},
			wantEr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := &MonitoringTestConfig{NodeAddressPreference: tt.pref}
			err := config.Validate()
			if tt.wantEr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestMonitoringTestConfigNodeAddressTypes(t *testing.T) {
	config := &MonitoringTestConfig{}
	config.ApplyDefaults()
	require.Equal(t, []corev1.NodeAddressType{corev1.NodeExternalIP, corev1.NodeInternalIP}, config.NodeAddressTypes())
}

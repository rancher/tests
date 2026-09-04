package monitoring

import (
	"fmt"

	corev1 "k8s.io/api/core/v1"
)

const (
	// ConfigurationFileKey is the top-level key used to unmarshal monitoringTest config from the Rancher config file.
	ConfigurationFileKey = "monitoringTest"

	// DefaultWebhookReceiverImage is the pinned webhook receiver image used when no override is configured. Never a floating tag.
	DefaultWebhookReceiverImage = "traefik:v3.7.12"

	// NodeAddressTypeExternal and NodeAddressTypeInternal are the allowed nodeAddressPreference entries.
	NodeAddressTypeExternal = "ExternalIP"
	NodeAddressTypeInternal = "InternalIP"
)

// MonitoringTestConfig holds configuration for monitoring validation tests.
type MonitoringTestConfig struct {
	// WebhookReceiverImage overrides the webhook receiver (traefik) image, e.g. an internal mirror in airgap environments.
	WebhookReceiverImage string `json:"webhookReceiverImage,omitempty" yaml:"webhookReceiverImage,omitempty"`
	// SkipWebhookReceiver skips the webhook receiver portion of the monitoring test (log-and-skip; suite continues).
	SkipWebhookReceiver bool `json:"skipWebhookReceiver,omitempty" yaml:"skipWebhookReceiver,omitempty"`
	// NodeAddressPreference is the ordered node address type preference used to reach the webhook receiver NodePort. Default: ExternalIP then InternalIP.
	NodeAddressPreference []string `json:"nodeAddressPreference,omitempty" yaml:"nodeAddressPreference,omitempty"`
}

// ApplyDefaults fills unset fields with their default values.
func (c *MonitoringTestConfig) ApplyDefaults() {
	if c.WebhookReceiverImage == "" {
		c.WebhookReceiverImage = DefaultWebhookReceiverImage
	}
	if len(c.NodeAddressPreference) == 0 {
		c.NodeAddressPreference = []string{NodeAddressTypeExternal, NodeAddressTypeInternal}
	}
}

// Validate returns an error when the config contains unsupported values; nil otherwise.
func (c *MonitoringTestConfig) Validate() error {
	for _, entry := range c.NodeAddressPreference {
		if entry != NodeAddressTypeExternal && entry != NodeAddressTypeInternal {
			return fmt.Errorf("invalid nodeAddressPreference entry %q: must be %q or %q", entry, NodeAddressTypeExternal, NodeAddressTypeInternal)
		}
	}

	return nil
}

// NodeAddressTypes converts NodeAddressPreference strings to corev1.NodeAddressType values. Callers must run ApplyDefaults first.
func (c *MonitoringTestConfig) NodeAddressTypes() []corev1.NodeAddressType {
	types := make([]corev1.NodeAddressType, 0, len(c.NodeAddressPreference))
	for _, entry := range c.NodeAddressPreference {
		types = append(types, corev1.NodeAddressType(entry))
	}

	return types
}

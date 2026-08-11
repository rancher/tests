package neuvector

const (
	// ConfigurationFileKey is the top-level key used to unmarshal neuvectorTest config from the Rancher config file.
	ConfigurationFileKey = "neuvectorTest"

	// DefaultUIPluginChartsURL is the public ui-plugin-charts repository used when no override is configured.
	DefaultUIPluginChartsURL = "https://github.com/rancher/ui-plugin-charts"
	// DefaultUIPluginChartsBranch is the ui-plugin-charts branch used when no override is configured.
	DefaultUIPluginChartsBranch = "main"
)

// NeuVectorTestConfig holds configuration for NeuVector validation tests.
type NeuVectorTestConfig struct {
	// UIPluginChartsURL overrides the git repository used for the UI plugin charts ClusterRepo (e.g. an internal mirror in airgap environments).
	UIPluginChartsURL string `json:"uiPluginChartsURL,omitempty" yaml:"uiPluginChartsURL,omitempty"`
	// UIPluginChartsBranch overrides the git branch of the UI plugin charts repository.
	UIPluginChartsBranch string `json:"uiPluginChartsBranch,omitempty" yaml:"uiPluginChartsBranch,omitempty"`
	// SkipUIExtension skips ClusterRepo creation and UI extension install entirely (e.g. airgap environments where the Rancher server cannot reach github.com and no uiPluginChartsURL mirror is configured). Not required when the extension is already installed; that case is auto-detected.
	SkipUIExtension bool `json:"skipUIExtension,omitempty" yaml:"skipUIExtension,omitempty"`
}

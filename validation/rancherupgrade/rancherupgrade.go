// Package rancherupgrade validates the Rancher server upgrade path (2.14.x -> latest)
// with interoperability products installed. Each phase (pre-upgrade, post-upgrade,
// chart-upgrade) is a suite test method selected via the -run regex and runs as its
// own go test invocation, so all cross-phase state lives in-cluster under the
// deterministic names defined here.
package rancherupgrade

const (
	// UpgradeInputConfigKey is the cattle-config key holding upgrade-phase inputs.
	UpgradeInputConfigKey = "rancherUpgradeInput"

	// PipelineConfigKey marks a config as authored by the rancher-upgrade
	// pipeline; generic PIT legs never set it.
	PipelineConfigKey = "rancherUpgradePipeline"
)

// UpgradeInput holds per-run upgrade parameters. TargetVersion is the Rancher
// version the server is upgraded to; written into the config by the pipeline
// (qa-infra-automation resolver output) or by hand for local runs.
type UpgradeInput struct {
	TargetVersion string `json:"targetVersion" yaml:"targetVersion"`
}

// PipelineInput is the marker section the rancher-upgrade pipeline writes into
// its cattle-config; the suite fails rather than skips when it is present but
// the upgrade target is missing (pipeline bug, not a generic PIT leg).
type PipelineInput struct {
	Enabled bool `json:"enabled" yaml:"enabled"`
}

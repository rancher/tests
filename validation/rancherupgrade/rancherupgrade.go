//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

// Package rancherupgrade validates the Rancher server upgrade path (2.14.x -> latest)
// with interoperability products installed. Each phase (pre-upgrade, post-upgrade,
// chart-upgrade) is a suite test method selected via the -run regex and runs as its
// own go test invocation, so all cross-phase state lives in-cluster under the
// deterministic names defined here.
package rancherupgrade

import "github.com/rancher/shepherd/pkg/config"

const (
	// UpgradeInputConfigKey is the cattle-config key holding upgrade-phase inputs.
	UpgradeInputConfigKey = "rancherUpgradeInput"

	// PipelineConfigKey marks a config as authored by the rancher-upgrade
	// pipeline; generic PIT legs never set it.
	PipelineConfigKey = "rancherUpgradePipeline"
)

const (
	// checksumWorkloadName is the deterministic name shared by the PVC and the nginx
	// deployment created in the pre-upgrade phase and relocated by later phases.
	checksumWorkloadName = "rancher-upgrade-longhorn"

	// checksumContent is the shell-safe data written to the checksum file on the
	// Longhorn-backed volume before the Rancher upgrade and verified after it.
	checksumContent = "rancher-upgrade-longhorn-checksum-v1"
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

// loadUpgradeInput returns the rancherUpgradeInput section of the test config.
func loadUpgradeInput() *UpgradeInput {
	upgradeInput := new(UpgradeInput)
	config.LoadConfig(UpgradeInputConfigKey, upgradeInput)

	return upgradeInput
}

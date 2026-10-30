//go:build (validation || pit.daily || pit.elemental || pit.harvester.daily) && !airgap.pit

package longhorn

const (
	// LonghornEncryptionSecretBaseName is the base name of the secret holding the
	// Longhorn volume-encryption crypto key.
	LonghornEncryptionSecretBaseName = "longhorn-crypto"

	// LonghornEncryptionStorageClassBaseName is the base name of the storage class
	// created with Longhorn volume encryption enabled.
	LonghornEncryptionStorageClassBaseName = "longhorn-crypto-global"

	// volumeBytesPrometheusQueryTemplate queries a single volume's capacity in
	// bytes through the Prometheus datasource.
	volumeBytesPrometheusQueryTemplate = "longhorn_volume_capacity_bytes{volume=\"%s\"}"

	// numberVolumesPrometheusQuery counts Longhorn volumes; the OR on() vector(0)
	// returns an empty list instead of nil.
	numberVolumesPrometheusQuery = "count(longhorn_volume_capacity_bytes) OR on() vector(0)"

	// skipTearDownConfigKey asks this suite to skip teardown: the rancher-upgrade
	// pipeline runs it as pre-upgrade coverage whose resources must survive the
	// in-place server upgrade (the pipeline's env teardown is the cleanup).
	skipTearDownConfigKey = "skipTearDown"
)

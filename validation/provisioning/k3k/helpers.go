package k3k

// This file contains helper functions for k3k cluster provisioning tests.
// These are placeholder implementations that document the expected structure
// and will need to be implemented once k3k APIs are available in Rancher.

// K3KClusterConfig represents the configuration for a k3k cluster.
// TODO: Define this structure based on the actual k3k API when available.
// This configuration will be used to specify all parameters needed
// to create a k3k cluster through Rancher's provisioning API.
type K3KClusterConfig struct {
	// Name of the k3k cluster
	Name string

	// Namespace where the k3k cluster will be created
	Namespace string

	// Mode specifies whether to use "shared" or "virtual" mode
	// - shared: Multiple clusters share the same underlying resources
	// - virtual: Each cluster has dedicated K3s server pods
	Mode string

	// KubernetesVersion is the version of K3s to use
	KubernetesVersion string

	// ResourceLimits defines CPU and memory limits for the cluster
	ResourceLimits *ResourceLimits

	// NodeCount is the number of nodes in the cluster
	NodeCount int

	// StorageClass for persistent volumes
	StorageClass string

	// Labels and annotations for the cluster
	Labels      map[string]string
	Annotations map[string]string
}

// ResourceLimits defines resource constraints for k3k clusters.
// This structure will be used to specify CPU and memory limits
// when provisioning k3k clusters once the API is available.
type ResourceLimits struct {
	CPU    string
	Memory string
}

// K3KClusterStatus represents the status of a k3k cluster.
// This structure will be used to track the state of k3k clusters
// and determine when they are ready for use.
type K3KClusterStatus struct {
	// Phase indicates the current phase (e.g., Pending, Running, Failed)
	Phase string

	// Ready indicates if the cluster is ready for use
	Ready bool

	// Message provides additional status information
	Message string

	// Mode shows the actual mode the cluster is running in
	Mode string
}

// Helper Functions (to be implemented when APIs are available)

// NewK3KClusterConfig creates a new k3k cluster configuration
// TODO: Implement when k3k APIs are available
// This should follow the pattern of clusters.NewK3SRKE2ClusterConfig
func NewK3KClusterConfig(name, namespace string, mode string) *K3KClusterConfig {
	return &K3KClusterConfig{
		Name:      name,
		Namespace: namespace,
		Mode:      mode,
		// Default values
		NodeCount:    1,
		StorageClass: "default",
		Labels:       make(map[string]string),
		Annotations:  make(map[string]string),
	}
}

// SetResourceLimits sets CPU and memory limits for the cluster
func (c *K3KClusterConfig) SetResourceLimits(cpu, memory string) {
	c.ResourceLimits = &ResourceLimits{
		CPU:    cpu,
		Memory: memory,
	}
}

// SetKubernetesVersion sets the K3s version for the cluster
func (c *K3KClusterConfig) SetKubernetesVersion(version string) {
	c.KubernetesVersion = version
}

// Notes for implementation:
//
// When k3k APIs become available, the following functions will need to be created
// in the appropriate packages:
//
// In github.com/rancher/shepherd/extensions/clusters:
// - CreateK3KCluster(client *rancher.Client, config *K3KClusterConfig) (*v1.SteveAPIObject, error)
// - DeleteK3KCluster(client *rancher.Client, clusterID string) error
// - GetK3KClusterStatus(client *rancher.Client, clusterID string) (*K3KClusterStatus, error)
// - WaitForK3KClusterReady(client *rancher.Client, clusterID string, timeout time.Duration) error
//
// In github.com/rancher/tests/actions/provisioning or similar:
// - VerifyK3KClusterReady(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject)
// - VerifyK3KResourceIsolation(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject)
// - VerifyK3KMode(t *testing.T, client *rancher.Client, cluster *v1.SteveAPIObject, expectedMode string)
//
// Configuration structure (to be added to provisioning input):
// type K3KConfig struct {
//     Mode              string
//     ResourceLimits    *ResourceLimits
//     StorageClass      string
//     EnableMonitoring  bool
// }
//
// API endpoints to check for when implementing:
// - GET    /v1/provisioning.cattle.io.clusters (should list k3k clusters)
// - POST   /v1/provisioning.cattle.io.clusters (should create k3k cluster)
// - GET    /v1/provisioning.cattle.io.clusters/{id} (should get k3k cluster details)
// - PUT    /v1/provisioning.cattle.io.clusters/{id} (should update k3k cluster)
// - DELETE /v1/provisioning.cattle.io.clusters/{id} (should delete k3k cluster)
//
// The cluster spec should include a field indicating the cluster type as "k3k"
// similar to how rke2/k3s clusters are differentiated.

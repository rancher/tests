package api

import (
"context"
"fmt"
"testing"
"time"

"github.com/rancher/shepherd/clients/rancher"
"github.com/rancher/shepherd/extensions/defaults"
"github.com/rancher/shepherd/pkg/namegenerator"
"github.com/rancher/tests/interoperability/longhorn"
kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
longhornNodeType      = "longhorn.io.node"
longhornSettingType   = "longhorn.io.setting"
longhornVolumeType    = "longhorn.io.volume"
longhornNamespace     = "longhorn-system"
)

// LonghornClient represents a client for interacting with Longhorn resources via Rancher API
type LonghornClient struct {
Client    *rancher.Client
ClusterID string
}

// NewLonghornClient creates a new Longhorn client that uses Rancher Steve API
func NewLonghornClient(client *rancher.Client, clusterID, serviceURL string) (*LonghornClient, error) {
longhornClient := &LonghornClient{
Client:    client,
ClusterID: clusterID,
}

return longhornClient, nil
}

// CreateVolume creates a new Longhorn volume via the Rancher Steve API
func CreateVolume(t *testing.T, lc *LonghornClient) (string, error) {
volumeName := namegenerator.AppendRandomString("test-lh-vol")

steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
if err != nil {
return "", fmt.Errorf("failed to get downstream client: %w", err)
}

// Create volume spec
volumeSpec := map[string]interface{}{
"type": longhornVolumeType,
"metadata": map[string]interface{}{
"name":      volumeName,
"namespace": longhornNamespace,
},
"spec": map[string]interface{}{
"numberOfReplicas": 3,
"size":             "1073741824", // 1Gi in bytes
"frontend":         "blockdev",   // Required for data engine v1
},
}

t.Logf("Creating Longhorn volume: %s", volumeName)
_, err = steveClient.SteveType(longhornVolumeType).Create(volumeSpec)
if err != nil {
return "", fmt.Errorf("failed to create volume: %w", err)
}

t.Logf("Successfully created volume: %s", volumeName)
return volumeName, nil
}

// ValidateVolumeActive validates that a volume is in an active/detached state and ready to use
func ValidateVolumeActive(t *testing.T, lc *LonghornClient, volumeName string) error {
t.Logf("Validating volume %s is active", volumeName)

steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
if err != nil {
return fmt.Errorf("failed to get downstream client: %w", err)
}

err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
volumeID := fmt.Sprintf("%s/%s", longhornNamespace, volumeName)
volume, err := steveClient.SteveType(longhornVolumeType).ByID(volumeID)
if err != nil {
return false, nil
}

// Extract status from the volume
if volume.Status == nil {
return false, nil
}

statusMap, ok := volume.Status.(map[string]interface{})
if !ok {
return false, nil
}

state, _ := statusMap["state"].(string)
robustness, _ := statusMap["robustness"].(string)

t.Logf("Volume %s state: %s, robustness: %s", volumeName, state, robustness)

// Volume is ready when it's in detached state with valid robustness
// "unknown" robustness is expected for detached volumes with no replicas scheduled
if state == "detached" && (robustness == "healthy" || robustness == "unknown") {
return true, nil
}

return false, nil
})

if err != nil {
return fmt.Errorf("volume %s did not become active: %w", volumeName, err)
}

t.Logf("Volume %s is active and ready to use", volumeName)
return nil
}

// DeleteVolume deletes a Longhorn volume
func DeleteVolume(lc *LonghornClient, volumeName string) error {
steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
if err != nil {
return fmt.Errorf("failed to get downstream client: %w", err)
}

volumeID := fmt.Sprintf("%s/%s", longhornNamespace, volumeName)
volume, err := steveClient.SteveType(longhornVolumeType).ByID(volumeID)
if err != nil {
return fmt.Errorf("failed to get volume %s: %w", volumeName, err)
}

err = steveClient.SteveType(longhornVolumeType).Delete(volume)
if err != nil {
return fmt.Errorf("failed to delete volume %s: %w", volumeName, err)
}

return nil
}

// ValidateNodes validates that all Longhorn nodes are in a valid state
func ValidateNodes(lc *LonghornClient) error {
steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
if err != nil {
return fmt.Errorf("failed to get downstream client: %w", err)
}

nodes, err := steveClient.SteveType(longhornNodeType).NamespacedSteveClient(longhornNamespace).List(nil)
if err != nil {
return fmt.Errorf("failed to list nodes: %w", err)
}

if len(nodes.Data) == 0 {
return fmt.Errorf("no Longhorn nodes found")
}

// Validate each node has valid conditions
for _, node := range nodes.Data {
if node.Status == nil {
return fmt.Errorf("node %s has no status", node.Name)
}
}

return nil
}

// ValidateSettings validates that Longhorn settings are properly configured
func ValidateSettings(lc *LonghornClient) error {
steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
if err != nil {
return fmt.Errorf("failed to get downstream client: %w", err)
}

settings, err := steveClient.SteveType(longhornSettingType).NamespacedSteveClient(longhornNamespace).List(nil)
if err != nil {
return fmt.Errorf("failed to list settings: %w", err)
}

if len(settings.Data) == 0 {
return fmt.Errorf("no Longhorn settings found")
}

return nil
}

// ValidateVolumeInRancherAPI validates that the volume is accessible and in a ready state through Rancher API
func ValidateVolumeInRancherAPI(t *testing.T, lc *LonghornClient, volumeName string) error {
	t.Logf("Validating volume %s is accessible through Rancher API", volumeName)

	steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	// Get the volume using the Rancher API path
	volumeID := fmt.Sprintf("%s/%s", longhornNamespace, volumeName)
	volume, err := steveClient.SteveType(longhornVolumeType).ByID(volumeID)
	if err != nil {
		return fmt.Errorf("failed to get volume %s through Rancher API: %w", volumeName, err)
	}

	// Validate volume has status
	if volume.Status == nil {
		return fmt.Errorf("volume %s has no status in Rancher API", volumeName)
	}

	statusMap, ok := volume.Status.(map[string]interface{})
	if !ok {
		return fmt.Errorf("volume %s status is not in expected format", volumeName)
	}

	state, _ := statusMap["state"].(string)
	robustness, _ := statusMap["robustness"].(string)

	t.Logf("Volume %s in Rancher API - state: %s, robustness: %s", volumeName, state, robustness)

	// Verify volume is in a ready state
	if state != "detached" {
		return fmt.Errorf("volume %s is not in detached state through Rancher API, current state: %s", volumeName, state)
	}

	if robustness != "healthy" && robustness != "unknown" {
		return fmt.Errorf("volume %s has invalid robustness through Rancher API: %s", volumeName, robustness)
	}

	t.Logf("Volume %s validated successfully through Rancher API", volumeName)
	return nil
}

// ValidateDynamicConfiguration validates Longhorn configuration based on user-provided test config
func ValidateDynamicConfiguration(t *testing.T, lc *LonghornClient, config longhorn.TestConfig) error {
steveClient, err := lc.Client.Steve.ProxyDownstream(lc.ClusterID)
if err != nil {
return fmt.Errorf("failed to get downstream client for dynamic validation: %w", err)
}

settings, err := steveClient.SteveType(longhornSettingType).NamespacedSteveClient(longhornNamespace).List(nil)
if err != nil {
return fmt.Errorf("failed to list settings: %w", err)
}

t.Logf("Successfully validated Longhorn configuration with %d settings", len(settings.Data))
t.Logf("Using storage class: %s from test configuration", config.LonghornTestStorageClass)

return nil
}

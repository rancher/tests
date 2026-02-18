package api

import (
	"context"
	"fmt"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	LonghornNodeType    = "longhorn.io.node"
	LonghornSettingType = "longhorn.io.setting"
	LonghornVolumeType  = "longhorn.io.volume"
)

// getReplicaCount determines an appropriate replica count for a Longhorn volume
// based on the number of available Longhorn nodes in the given namespace.
func getReplicaCount(client *rancher.Client, clusterID, namespace string) (int, error) {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return 0, fmt.Errorf("failed to get downstream client for replica count: %w", err)
	}

	longhornNodes, err := steveClient.SteveType(LonghornNodeType).NamespacedSteveClient(namespace).List(nil)
	if err != nil {
		return 0, fmt.Errorf("failed to list Longhorn nodes: %w", err)
	}

	nodeCount := len(longhornNodes.Data)
	if nodeCount <= 0 {
		return 0, fmt.Errorf("no Longhorn nodes found in namespace %s", namespace)
	}

	return nodeCount, nil
}

// CreateVolume creates a new Longhorn volume via the Rancher Steve API and returns a pointer to it
func CreateVolume(client *rancher.Client, clusterID, namespace string) (*steveV1.SteveAPIObject, error) {
	volumeName := namegenerator.AppendRandomString("test-lh-vol")

	replicaCount, err := getReplicaCount(client, clusterID, namespace)
	if err != nil {
		return nil, err
	}

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get downstream client: %w", err)
	}

	// Create volume spec
	volumeSpec := map[string]interface{}{
		"type": LonghornVolumeType,
		"metadata": map[string]interface{}{
			"name":      volumeName,
			"namespace": namespace,
		},
		"spec": map[string]interface{}{
			"numberOfReplicas": replicaCount,
			"size":             "1073741824", // 1Gi in bytes
			// blockdev frontend is required for Longhorn data engine v1, which is the default storage engine
			// that uses Linux kernel block devices to manage volumes
			"frontend": "blockdev",
		},
	}

	logrus.Infof("Creating Longhorn volume: %s with %d replicas", volumeName, replicaCount)
	volume, err := steveClient.SteveType(LonghornVolumeType).Create(volumeSpec)
	if err != nil {
		return nil, fmt.Errorf("failed to create volume: %w", err)
	}

	logrus.Infof("Successfully created volume: %s", volumeName)
	
	// Register cleanup function for the volume
	client.Session.RegisterCleanupFunc(func() error {
		logrus.Infof("Cleaning up test volume: %s", volumeName)
		return DeleteVolume(client, clusterID, namespace, volumeName)
	})
	
	return volume, nil
}

// ValidateVolumeActive validates that a volume is in an active/detached state and ready to use
func ValidateVolumeActive(client *rancher.Client, clusterID, namespace, volumeName string) error {
	logrus.Infof("Validating volume %s is active", volumeName)

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		volumeID := fmt.Sprintf("%s/%s", namespace, volumeName)
		volume, err := steveClient.SteveType(LonghornVolumeType).ByID(volumeID)
		if err != nil {
			// Ignore error and continue polling as volume may not be available immediately
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

		logrus.Infof("Volume %s state: %s, robustness: %s", volumeName, state, robustness)

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

	logrus.Infof("Volume %s is active and ready to use", volumeName)
	return nil
}

// DeleteVolume deletes a Longhorn volume
func DeleteVolume(client *rancher.Client, clusterID, namespace, volumeName string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	volumeID := fmt.Sprintf("%s/%s", namespace, volumeName)
	volume, err := steveClient.SteveType(LonghornVolumeType).ByID(volumeID)
	if err != nil {
		return fmt.Errorf("failed to get volume %s: %w", volumeName, err)
	}

	logrus.Infof("Deleting volume: %s", volumeName)
	err = steveClient.SteveType(LonghornVolumeType).Delete(volume)
	if err != nil {
		return fmt.Errorf("failed to delete volume %s: %w", volumeName, err)
	}

	return nil
}

// ValidateNodes validates that all Longhorn nodes are in a valid state
// This check is performed immediately without polling because nodes should already be
// in a ready state before Longhorn installation completes
func ValidateNodes(client *rancher.Client, clusterID, namespace string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	nodes, err := steveClient.SteveType(LonghornNodeType).NamespacedSteveClient(namespace).List(nil)
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
// Checks that at least one setting has a non-nil value to ensure settings are accessible
func ValidateSettings(client *rancher.Client, clusterID, namespace string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	settings, err := steveClient.SteveType(LonghornSettingType).NamespacedSteveClient(namespace).List(nil)
	if err != nil {
		return fmt.Errorf("failed to list settings: %w", err)
	}

	if len(settings.Data) == 0 {
		return fmt.Errorf("no Longhorn settings found")
	}

	// Validate that at least one setting has a value field
	hasValidSetting := false
	for _, setting := range settings.Data {
		if setting.JSONResp != nil {
			if valueMap, ok := setting.JSONResp.(map[string]interface{}); ok {
				if _, exists := valueMap["value"]; exists {
					hasValidSetting = true
					break
				}
			}
		}
	}

	if !hasValidSetting {
		return fmt.Errorf("no Longhorn settings have valid value fields")
	}

	return nil
}

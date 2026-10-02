package imported

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	rancherv1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	etcdSnapshotSaveKind    = "ETCDSnapshotSave"
	etcdSnapshotRestoreKind = "ETCDSnapshotRestore"
	managementAPIVersion    = "management.cattle.io/v3"
	v1alpha1APIVersion      = "operation.cattle.io/v1alpha1"

	etcdSnapshotSaveSteveType    = "operation.cattle.io.etcdsnapshotsave"
	etcdSnapshotRestoreSteveType = "operation.cattle.io.etcdsnapshotrestore"
)

// CreateImportedETCDSnapshot is a helper function that creates an ETCD snapshot for an imported cluster.
func CreateImportedETCDSnapshot(client *rancher.Client, clusterName string) (string, error) {
	snapshotQuery, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", etcdsnapshot.SnapshotClusterNameLabel, clusterName))
	if err != nil {
		return "", err
	}

	snapshotClient := client.Steve.SteveType(etcdsnapshot.SnapshotSteveResourceType)
	existingSnapshots, err := snapshotClient.List(snapshotQuery)
	if err != nil {
		return "", err
	}

	existingSnapshotIDs := make(map[string]struct{}, len(existingSnapshots.Data))
	for _, snapshot := range existingSnapshots.Data {
		existingSnapshotIDs[snapshot.ID] = struct{}{}
	}

	snapshot := map[string]any{
		"apiVersion": v1alpha1APIVersion,
		"kind":       etcdSnapshotSaveKind,
		"metadata": map[string]any{
			"generateName": clusterName + "-",
			"namespace":    clusterName,
		},
		"spec": map[string]any{
			"cancel": false,
			"clusterRef": map[string]any{
				"apiVersion": managementAPIVersion,
				"kind":       "Cluster",
				"name":       clusterName,
			},
			"ttl": 60,
		},
	}

	resourceType := client.Steve.SteveType(etcdSnapshotSaveSteveType)
	created, err := resourceType.NamespacedSteveClient(clusterName).Create(snapshot)
	if err != nil {
		return "", err
	}

	if err := waitForSnapshotOperation(client, etcdSnapshotSaveSteveType, created.ID); err != nil {
		return "", err
	}

	var createdSnapshotIDs []string
	var createdSnapshotNames []string
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FifteenMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		snapshots, err := snapshotClient.List(snapshotQuery)
		if err != nil {
			return false, nil
		}

		createdSnapshotIDs = createdSnapshotIDs[:0]
		createdSnapshotNames = createdSnapshotNames[:0]
		for _, snapshot := range snapshots.Data {
			if snapshot.Labels[etcdsnapshot.SnapshotClusterNameLabel] != clusterName {
				continue
			}

			if snapshot.CreationTimestamp.Time.Add(time.Second).Before(created.CreationTimestamp.Time) {
				continue
			}

			if _, exists := existingSnapshotIDs[snapshot.ID]; !exists {
				createdSnapshotIDs = append(createdSnapshotIDs, snapshot.ID)
				createdSnapshotNames = append(createdSnapshotNames, snapshot.Name)
			}
		}

		return len(createdSnapshotIDs) > 0, nil
	})
	if err != nil {
		return "", err
	}

	if err := etcdsnapshot.VerifyV2ProvSnapshots(client, clusterName, createdSnapshotIDs); err != nil {
		return "", err
	}

	return createdSnapshotNames[0], nil
}

// RestoreImportedETCDSnapshot restores the named snapshot on an imported cluster.
func RestoreImportedETCDSnapshot(client *rancher.Client, clusterName, snapshotName string) error {
	if clusterName == "" || snapshotName == "" {
		return fmt.Errorf("cluster name and snapshot name are required for imported snapshot restore")
	}

	if err := etcdsnapshot.VerifySnapshotReadyForRestore(client, clusterName, snapshotName); err != nil {
		return fmt.Errorf("verify snapshot %q belongs to cluster %q and is ready for restore: %w", snapshotName, clusterName, err)
	}

	restore := map[string]any{
		"apiVersion": v1alpha1APIVersion,
		"kind":       etcdSnapshotRestoreKind,
		"metadata": map[string]any{
			"generateName": clusterName + "-",
			"namespace":    clusterName,
		},
		"spec": map[string]any{
			"args": map[string]any{
				"name": snapshotName,
			},
			"cancel": false,
			"clusterRef": map[string]any{
				"apiVersion": managementAPIVersion,
				"kind":       "Cluster",
				"name":       clusterName,
			},
			"ttl": 60,
		},
	}

	resourceType := client.Steve.SteveType(etcdSnapshotRestoreSteveType)
	created, err := resourceType.NamespacedSteveClient(clusterName).Create(restore)
	if err != nil {
		return err
	}

	return waitForSnapshotOperation(client, etcdSnapshotRestoreSteveType, created.ID)
}

func waitForSnapshotOperation(client *rancher.Client, resourceType, operationID string) error {
	var lastStatus any

	operationClient := client.Steve.SteveType(resourceType)

	err := kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 30*time.Minute, true, func(ctx context.Context) (bool, error) {
		current, err := operationClient.ByID(operationID)
		if err != nil {
			return false, fmt.Errorf("get snapshot operation %s: %w", operationID, err)
		}

		lastStatus = current.Status

		return snapshotOperationResults(operationID, current.Status)
	})
	if err != nil {
		return fmt.Errorf("waiting for %s snapshot operation %s (last status: %v): %w", resourceType, operationID, lastStatus, err)
	}

	return nil
}

func snapshotOperationResults(operationID string, rawStatus any) (bool, error) {
	var status struct {
		Phase      string          `json:"phase"`
		Step       string          `json:"step"`
		Conditions json.RawMessage `json:"conditions"`
	}

	if err := rancherv1.ConvertToK8sType(rawStatus, &status); err != nil {
		return false, fmt.Errorf("decode snapshot operation results for %s status: %w", operationID, err)
	}

	switch status.Phase {
	case "Succeeded":
		return true, nil
	case "Failed", "Canceled":
		return false, fmt.Errorf("snapshot operation %s ended with phase %q at step %q; conditions: %s", operationID, status.Phase, status.Step, status.Conditions)
	default:
		return false, nil
	}
}

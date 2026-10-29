package imported

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/tests/actions/clusters/imported"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/storage/s3"
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

	if err := imported.WaitForOperation(client, etcdSnapshotSaveSteveType, created.ID); err != nil {
		return "", err
	}

	var createdSnapshotIDs []string
	var selectedSnapshotName string
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FifteenMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		snapshots, err := snapshotClient.List(snapshotQuery)
		if err != nil {
			return false, nil
		}

		createdSnapshotIDs = createdSnapshotIDs[:0]
		for _, snapshot := range snapshots.Data {
			if snapshot.Labels[etcdsnapshot.SnapshotClusterNameLabel] != clusterName {
				continue
			}

			if snapshot.CreationTimestamp.Time.Add(time.Second).Before(created.CreationTimestamp.Time) {
				continue
			}

			if _, exists := existingSnapshotIDs[snapshot.ID]; !exists {
				createdSnapshotIDs = append(createdSnapshotIDs, snapshot.ID)
				if selectedSnapshotName == "" || s3.CheckS3SnapshotLocation(snapshot) {
					selectedSnapshotName = snapshot.Name
				}
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

	return selectedSnapshotName, nil
}

// RestoreImportedETCDSnapshot restores the named snapshot on an imported cluster with an optional restore mode.
func RestoreImportedETCDSnapshot(client *rancher.Client, clusterName, snapshotName string, restoreMode ...string) error {
	if clusterName == "" || snapshotName == "" {
		return fmt.Errorf("cluster name and snapshot name are required for imported snapshot restore")
	}

	if len(restoreMode) > 1 {
		return fmt.Errorf("at most one restore mode may be supplied for imported snapshot restore")
	}

	args := map[string]any{"name": snapshotName}
	if len(restoreMode) == 1 && restoreMode[0] != "" {
		args["restoreMode"] = restoreMode[0]
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
			"args":   args,
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

	return imported.WaitForOperation(client, etcdSnapshotRestoreSteveType, created.ID)
}

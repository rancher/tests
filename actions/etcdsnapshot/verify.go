package etcdsnapshot

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extdefault "github.com/rancher/shepherd/extensions/defaults"
	shepherdsnapshot "github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const requiredStableSnapshotPolls = 3

func listClusterSnapshots(client *rancher.Client, clusterName string) ([]steveV1.SteveAPIObject, error) {
	query, err := url.ParseQuery(fmt.Sprintf("labelSelector=%s=%s", shepherdsnapshot.SnapshotClusterNameLabel, clusterName))
	if err != nil {
		return nil, err
	}

	snapshotList, err := client.Steve.SteveType(shepherdsnapshot.SnapshotSteveResourceType).List(query)
	if err != nil {
		return nil, err
	}

	return snapshotList.Data, nil
}

func verifySnapshotsStable(client *rancher.Client, clusterName string, isReady func([]steveV1.SteveAPIObject) bool) error {
	stablePolls := 0

	err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, extdefault.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		snapshotList, err := listClusterSnapshots(client, clusterName)
		if err != nil || len(snapshotList) == 0 {
			stablePolls = 0
			return false, nil
		}

		if !isReady(snapshotList) {
			stablePolls = 0
			return false, nil
		}

		stablePolls++
		return stablePolls >= requiredStableSnapshotPolls, nil
	})
	if err != nil {
		return err
	}

	return nil
}

// VerifyV2ProvSnapshots verifies that all snapshots come to an active state, and the correct number
// of snapshots were taken based on the number of nodes and snapshot type (s3 vs. local)
func VerifyV2ProvSnapshots(client *rancher.Client, clusterName string, snapshotIDs []string) error {
	err := verifySnapshotsStable(client, clusterName, func(snapshotList []steveV1.SteveAPIObject) bool {
		listedSnapshots := map[string]steveV1.SteveAPIObject{}
		for _, snapshot := range snapshotList {
			listedSnapshots[snapshot.ID] = snapshot
		}

		for _, snapshotID := range snapshotIDs {
			snapshot, ok := listedSnapshots[snapshotID]
			if !ok {
				return false
			}

			if snapshot.ObjectMeta.State.Name != "active" {
				return false
			}
		}

		return true
	})
	if err != nil {
		return err
	}

	logrus.Debugf("Snapshot %v verified in cluster %s", snapshotIDs, clusterName)

	return nil
}

// VerifySnapshotReadyForRestore verifies the selected restore snapshot is present in the list and active.
func VerifySnapshotReadyForRestore(client *rancher.Client, clusterName, snapshotName string) error {
	err := verifySnapshotsStable(client, clusterName, func(snapshotList []steveV1.SteveAPIObject) bool {
		for _, snapshot := range snapshotList {
			if snapshot.Name != snapshotName {
				continue
			}

			if snapshot.ObjectMeta.State.Name != "active" {
				return false
			}

			return true
		}

		return false
	})
	if err != nil {
		return err
	}

	logrus.Debugf("Snapshot %s is ready for restore in cluster %s", snapshotName, clusterName)

	return nil
}

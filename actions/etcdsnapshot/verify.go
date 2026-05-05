package etcdsnapshot

import (
	"context"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extdefault "github.com/rancher/shepherd/extensions/defaults"
	shepherdsnapshot "github.com/rancher/shepherd/extensions/etcdsnapshot"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// VerifyV2ProvSnapshots verifies that all snapshots come to an active state, and the correct number
// of snapshots were taken based on the number of nodes and snapshot type (s3 vs. local)
func VerifyV2ProvSnapshots(client *rancher.Client, clusterName string, snapshotIDs []string) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, extdefault.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		var snapshotObj any
		var state string

		for _, snapshotID := range snapshotIDs {
			snapshotObj, err = client.Steve.SteveType(shepherdsnapshot.SnapshotSteveResourceType).ByID(snapshotID)
			state = snapshotObj.(*steveV1.SteveAPIObject).ObjectMeta.State.Name

			if err != nil {
				return false, nil
			}

			if state != "active" {
				return false, nil
			}
		}
		return true, nil
	})
	if err != nil {
		return err
	}

	return nil
}

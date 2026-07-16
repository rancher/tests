//go:build (validation || extended || infra.any || cluster.any) && !sanity && !stress

package k3s

import (
	"fmt"
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/validation/snapshot"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type SnapshotRetentionConfig struct {
	SnapshotRetention int `json:"snapshotRetention" yaml:"snapshotRetention"`
}

func TestAutomaticSnapshotRetention(t *testing.T) {
	t.Parallel()

	s := snapshot.Setup(t, defaults.K3S, false, false)

	tests := []struct {
		name                     string
		cluster                  *v1.SteveAPIObject
		retentionLimit           int
		intervalBetweenSnapshots int
	}{
		{"K3S_Retention_Limit", s.Cluster, 2, 1},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.Session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			clusterObject, clusterResponse, err := extClusters.GetProvisioningClusterByName(s.Client, tt.cluster.Name, namespaces.FleetDefault)
			require.NoError(t, err)

			clusterObject.Spec.RKEConfig.ETCD.SnapshotRetention = tt.retentionLimit
			cronSchedule := fmt.Sprintf("%s%v%s", "*/", tt.intervalBetweenSnapshots, " * * * *")
			clusterObject.Spec.RKEConfig.ETCD.SnapshotScheduleCron = cronSchedule

			_, err = s.Client.Steve.SteveType(stevetypes.Provisioning).Update(clusterResponse, clusterObject)
			require.NoError(t, err)

			err = etcdsnapshot.CreateSnapshotsUntilRetentionLimit(s.Client, tt.cluster.Name, tt.retentionLimit, tt.intervalBetweenSnapshots)
			require.NoError(t, err)
		})
	}
}

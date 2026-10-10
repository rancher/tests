//go:build (validation || imported) && !pit.daily && !pit.weekly && !pit.event && !pit.harvester.daily && !pit.elemental

package k3s

import (
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/etcdsnapshot/imported"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	v3cluster "github.com/rancher/tests/validation/recurring/infrastructure/upgraderancher/v3cluster"
	"github.com/rancher/tests/validation/snapshot"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRestoreK8sUpgradeImported(t *testing.T) {
	t.Skip("Skipped due to https://github.com/rancher/rancher/issues/56077")
	t.Parallel()

	s := snapshot.ImportedSetup(t, defaults.K3S)

	snapshotRestore := &etcdsnapshot.Config{
		RestoreMode: "kubernetesVersion",
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		cluster      *v1.SteveAPIObject
	}{
		{"K3S_Imported_Restore_ETCD_K8sVersion", snapshotRestore, s.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.Session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var err error

			logrus.Infof("Creating an etcd snapshot on imported cluster (%s)", tt.cluster.Name)
			snapshotName, err := imported.CreateImportedETCDSnapshot(s.Client, tt.cluster.Name)
			require.NoError(t, err)

			clusterStatus := &provv1.ClusterStatus{}
			err = v1.ConvertToK8sType(tt.cluster.Status, clusterStatus)
			require.NoError(t, err)

			clusterResp, err := s.Client.Management.Cluster.ByID(clusterStatus.ClusterName)
			require.NoError(t, err)

			initialVersion := clusterResp.Version.GitVersion
			require.NotEmpty(t, initialVersion)

			logrus.Infof("Upgrading imported cluster (%s)", tt.cluster.Name)
			err = v3cluster.UpgradeV3Cluster(s.Client, clusterResp.Name)
			require.NoError(t, err)

			logrus.Infof("Restoring the etcd snapshot on imported cluster (%s)", tt.cluster.Name)
			err = imported.RestoreImportedETCDSnapshot(s.Client, tt.cluster.Name, snapshotName, tt.etcdSnapshot.RestoreMode)
			require.NoError(t, err)

			updatedClusteResp, err := s.Client.Management.Cluster.ByID(clusterResp.ID)
			require.NoError(t, err)

			restoredVersion := updatedClusteResp.Version.GitVersion
			require.NotEmpty(t, restoredVersion)
			require.Equal(t, initialVersion, restoredVersion)

			logrus.Infof("Verifying the imported cluster is ready (%s)", tt.cluster.Name)
			require.NoError(t, provisioning.VerifyClusterReadyV3(s.Client, tt.cluster.Name))

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(s.Client, tt.cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(s.Client, s.CattleConfig)
		if err := qase.UpdateSchemaParameters(tt.name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

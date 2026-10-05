//go:build validation || imported

package k3s

import (
	"os"
	"testing"

	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot/imported"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/snapshot"
	"github.com/rancher/tfp-automation/framework/cleanup"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRestoreImported(t *testing.T) {
	t.Parallel()

	s := snapshot.ImportedSetup(t, defaults.K3S)

	for _, tt := range s.Clusters {
		name := "K3S_Imported_Restore_ETCD|" + tt.Name
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s/%s)", name, tt.Cluster.Name)
			if tt.TerraformOptions != nil {
				cleanup.Cleanup(t, tt.TerraformOptions, tt.NestedModuleDir)
				if tt.NestedModuleDir != "" {
					if err := os.RemoveAll(tt.NestedModuleDir); err != nil {
						logrus.Warningf("Failed to remove module directory %s: %v", tt.NestedModuleDir, err)
					}
				}
			}
		})

		t.Run(name, func(t *testing.T) {
			t.Parallel()

			var err error

			logrus.Infof("Creating an etcd snapshot on imported cluster (%s)", tt.Cluster.Name)
			snapshotName, err := imported.CreateImportedETCDSnapshot(s.Client, tt.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Restoring the etcd snapshot on imported cluster (%s)", tt.Cluster.Name)
			err = imported.RestoreImportedETCDSnapshot(s.Client, tt.Cluster.Name, snapshotName)
			require.NoError(t, err)

			logrus.Infof("Verifying the imported cluster is ready (%s)", tt.Cluster.Name)
			require.NoError(t, provisioning.VerifyClusterReadyV3(s.Client, tt.Cluster.Name))

			logrus.Infof("Verifying cluster deployments (%s)", tt.Cluster.Name)
			err = deployment.VerifyClusterDeployments(s.Client, tt.Cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.Cluster.Name)
			err = pods.VerifyClusterPods(s.Client, tt.Cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(s.Client, s.CattleConfig)
		if err := qase.UpdateSchemaParameters(name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

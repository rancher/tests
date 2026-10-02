//go:build validation || imported

package k3s

import (
	"os"
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
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

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"K3S_Imported_Restore_ETCD", s.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			cleanup.Cleanup(t, s.TerraformOptions, s.NestedModuleDir)
			os.RemoveAll(s.NestedModuleDir)
			s.Session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			var err error

			logrus.Infof("Creating an etcd snapshot on imported cluster (%s)", s.Cluster.Name)
			snapshotName, err := imported.CreateImportedETCDSnapshot(s.Client, s.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Restoring the etcd snapshot on imported cluster (%s)", s.Cluster.Name)
			err = imported.RestoreImportedETCDSnapshot(s.Client, s.Cluster.Name, snapshotName)
			require.NoError(t, err)

			logrus.Infof("Verifying the imported cluster is ready (%s)", s.Cluster.Name)
			require.NoError(t, provisioning.VerifyClusterReadyV3(s.Client, s.Cluster.Name))

			logrus.Infof("Verifying cluster deployments (%s)", s.Cluster.Name)
			err = deployment.VerifyClusterDeployments(s.Client, s.Cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", s.Cluster.Name)
			err = pods.VerifyClusterPods(s.Client, s.Cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(s.Client, s.CattleConfig)
		if err := qase.UpdateSchemaParameters(tt.name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

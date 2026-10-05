//go:build validation || imported

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/tests/actions/config/defaults"
	importedRotation "github.com/rancher/tests/actions/encryptionkeyrotation/imported"
	importedSnapshot "github.com/rancher/tests/actions/etcdsnapshot/imported"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	ekrotation "github.com/rancher/tests/validation/encryptionkeyrotation"
	"github.com/rancher/tfp-automation/framework/cleanup"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestEncryptionKeyRotationImported(t *testing.T) {
	t.Parallel()

	e := ekrotation.ImportedSetup(t, defaults.RKE2)

	for _, tt := range e.Clusters {
		name := "RKE2_Imported_Encryption_Key_Rotation|" + tt.Name
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
			_, err = importedSnapshot.CreateImportedETCDSnapshot(e.Client, tt.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Performing encryption key rotation on cluster (%s)", tt.Cluster.Name)
			err = importedRotation.RotateEncryptionKey(e.Client, tt.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying the imported cluster is ready (%s)", tt.Cluster.Name)
			err = provisioning.VerifyClusterReadyV3(e.Client, tt.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.Cluster.Name)
			err = deployment.VerifyClusterDeployments(e.Client, tt.Cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.Cluster.Name)
			err = pods.VerifyClusterPods(e.Client, tt.Cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(e.Client, e.CattleConfig)
		if err := qase.UpdateSchemaParameters(name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

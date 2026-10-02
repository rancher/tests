//go:build validation || imported

package rke2

import (
	"os"
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
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

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"RKE2_Imported_Encryption_Key_Rotation", e.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			cleanup.Cleanup(t, e.TerraformOptions, e.NestedModuleDir)
			os.RemoveAll(e.NestedModuleDir)
			e.Session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			var err error

			logrus.Infof("Creating an etcd snapshot on imported cluster (%s)", e.Cluster.Name)
			_, err = importedSnapshot.CreateImportedETCDSnapshot(e.Client, e.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Performing encryption key rotation on cluster (%s)", e.Cluster.Name)
			err = importedRotation.RotateEncryptionKey(e.Client, e.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying the imported cluster is ready (%s)", e.Cluster.Name)
			err = provisioning.VerifyClusterReadyV3(e.Client, e.Cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", e.Cluster.Name)
			err = deployment.VerifyClusterDeployments(e.Client, e.Cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", e.Cluster.Name)
			err = pods.VerifyClusterPods(e.Client, e.Cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(e.Client, e.CattleConfig)
		if err := qase.UpdateSchemaParameters(tt.name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

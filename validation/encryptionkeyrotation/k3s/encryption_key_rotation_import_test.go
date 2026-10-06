//go:build validation || imported

package k3s

import (
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
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestEncryptionKeyRotationImported(t *testing.T) {
	t.Parallel()

	e := ekrotation.ImportedSetup(t, defaults.K3S)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"K3S_Imported_Encryption_Key_Rotation", e.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			e.Session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var err error

			logrus.Infof("Creating an etcd snapshot on imported cluster (%s)", tt.cluster.Name)
			_, err = importedSnapshot.CreateImportedETCDSnapshot(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Performing encryption key rotation on cluster (%s)", tt.cluster.Name)
			err = importedRotation.RotateEncryptionKey(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying the imported cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReadyV3(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(e.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(e.Client, tt.cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(e.Client, e.CattleConfig)
		if err := qase.UpdateSchemaParameters(tt.name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

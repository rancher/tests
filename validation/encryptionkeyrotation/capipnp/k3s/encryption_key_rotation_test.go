//go:build validation || capi

package k3s

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	capi "github.com/rancher/tests/actions/capipnp"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/encryptionkeyrotation"
	snapshot "github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/encryptionkeyrotation/capipnp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestCAPIPnPEncryptionKeyRotation(t *testing.T) {
	t.Parallel()

	e := capipnp.Setup(t, defaults.K3S)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"CAPI_PnP_K3S_Encryption_Key_Rotation", e.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Cleaning up cluster (%s)", e.Cluster.Name)
			cleanupErr := capi.DeleteCAPICluster(e.Client, e.Cluster.Name, e.ClusterManifest)
			require.NoError(t, cleanupErr)
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			logrus.Infof("Creating snapshot on cluster (%s)", tt.cluster.Name)
			_, err := snapshot.CreateRKE2K3SSnapshot(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Enabling secrets encryption on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.EnableSecretsEncryption(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Performing encryption key rotation on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.RotateEncryptionKey(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying encryption key rotated on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.VerifyRotationFromControlPlaneStatus(e.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(e.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(e.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(e.Client, tt.cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(e.Client, e.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

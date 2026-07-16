//go:build validation || recurring || proxy || ipv6 || dualstack

package rke2

import (
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	snapshot "github.com/rancher/shepherd/extensions/etcdsnapshot"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/encryptionkeyrotation"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	ekr "github.com/rancher/tests/validation/encryptionkeyrotation"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestEncryptionKeyRotation(t *testing.T) {
	t.Parallel()

	e := ekr.Setup(t, defaults.RKE2)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"RKE2_Encryption_Key_Rotation", e.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			e.Session.Cleanup()
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

			clusterStatus := &provv1.ClusterStatus{}
			err = steveV1.ConvertToK8sType(tt.cluster.Status, clusterStatus)
			require.NoError(t, err)

			logrus.Infof("Verifying encryption key rotated on cluster (%s)", tt.cluster.Name)
			err = encryptionkeyrotation.VerifyEncryptionKeyRotation(e.Client, clusterStatus, defaults.RKE2)
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

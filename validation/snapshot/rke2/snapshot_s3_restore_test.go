//go:build validation || recurring

package rke2

import (
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/snapshot"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestS3SnapshotRestore(t *testing.T) {
	t.Parallel()

	s := snapshot.Setup(t, defaults.RKE2, true, false)

	snapshotRestore := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		cluster      *v1.SteveAPIObject
	}{
		{"RKE2_S3_Restore", snapshotRestore, s.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.Session.Cleanup()
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			logrus.Infof("Creating snapshot on cluster %s", tt.cluster.Name)
			clusterObject, snapshotName, err := etcdsnapshot.CreateAndValidateSnapshotV2Prov(s.Client, tt.cluster.Name, tt.cluster.ID, tt.etcdSnapshot)
			require.NoError(t, err)

			clusterStatus := &provv1.ClusterStatus{}
			err = v1.ConvertToK8sType(tt.cluster.Status, clusterStatus)
			require.NoError(t, err)

			err = snapshot.CreateSnapshotDeployment(s.Client, s.WorkloadClient, clusterStatus.ClusterName, tt.cluster.Name, s.WorkloadsConfig)
			require.NoError(t, err)

			logrus.Infof("Restoring snapshot %s on cluster %s", snapshotName, tt.cluster.Name)
			_, err = etcdsnapshot.RestoreAndValidateSnapshotV2Prov(s.Client, snapshotName, tt.etcdSnapshot, clusterObject, tt.cluster.ID)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(s.Client, tt.cluster)
			require.NoError(t, err)

			err = etcdsnapshot.DeleteS3Bucket(s.S3BucketName, s.S3Region, s.AWSAccessKey, s.AWSSecretKey)
			assert.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.Client, s.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

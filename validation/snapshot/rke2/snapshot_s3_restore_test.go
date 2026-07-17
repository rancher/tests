//go:build validation || recurring

package rke2

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
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
			err = etcdsnapshot.CreateAndValidateSnapshotRestore(s.Client, tt.cluster.Name, tt.etcdSnapshot, snapshot.ContainerImage)
			require.NoError(t, err)

			if s.CreatedTestBucket && s.S3BucketName != "" {
				err := etcdsnapshot.DeleteS3Bucket(s.S3BucketName, s.S3Region, s.AWSAccessKey, s.AWSSecretKey)
				assert.NoError(t, err)
			}
		})

		params := provisioning.GetProvisioningSchemaParams(s.Client, s.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

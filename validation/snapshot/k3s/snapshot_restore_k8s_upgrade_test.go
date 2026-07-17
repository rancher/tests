//go:build (validation || recurring || proxy || ipv6 || dualstack || extended || infra.any || cluster.any || pit.weekly || pit.elemental) && !sanity && !stress

package k3s

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/validation/snapshot"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestSnapshotRestoreK8sUpgrade(t *testing.T) {
	t.Parallel()

	s := snapshot.Setup(t, defaults.K3S, false, false)

	snapshotRestore := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "kubernetesVersion",
		RecurringRestores:        1,
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		cluster      *v1.SteveAPIObject
	}{
		{"K3S_Restore_ETCD_K8sVersion", snapshotRestore, s.Cluster},
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
		})

		params := provisioning.GetProvisioningSchemaParams(s.Client, s.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

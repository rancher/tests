//go:build (validation || extended || infra.any || cluster.any) && !sanity && !stress

package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SnapshotRestoreExistingClusterTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	clusterObject *v1.SteveAPIObject
}

func (s *SnapshotRestoreExistingClusterTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRestoreExistingClusterTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)

	s.client = client

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.cattleConfig, err = defaults.LoadPackageDefaults(s.cattleConfig, "")
	require.NoError(s.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	s.clusterObject, err = client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
}

func snapshotRestoreExistingClusterConfigs() []*etcdsnapshot.Config {
	return []*etcdsnapshot.Config{
		{
			UpgradeKubernetesVersion: "",
			SnapshotRestore:          "none",
			RecurringRestores:        1,
		},
		{
			UpgradeKubernetesVersion: "",
			SnapshotRestore:          "kubernetesVersion",
			RecurringRestores:        1,
		},
		{
			UpgradeKubernetesVersion:     "",
			SnapshotRestore:              "all",
			ControlPlaneConcurrencyValue: "15%",
			WorkerConcurrencyValue:       "20%",
			RecurringRestores:            1,
		},
	}
}

func (s *SnapshotRestoreExistingClusterTestSuite) TestSnapshotRestoreExistingCluster() {
	snapshotRestoreConfig := snapshotRestoreExistingClusterConfigs()
	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		clusterID    string
	}{
		{"RKE2_Restore_ETCD", snapshotRestoreConfig[0], s.clusterObject.ID},
		{"RKE2_Restore_ETCD_K8sVersion", snapshotRestoreConfig[1], s.clusterObject.ID},
		{"RKE2K3S_Restore_Upgrade_Strategy", snapshotRestoreConfig[2], s.clusterObject.ID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			err := etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, cluster.Name, tt.etcdSnapshot, "nginx")
			require.NoError(s.T(), err)
		})
	}
}

func TestSnapshotRestoreExistingClusterTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRestoreExistingClusterTestSuite))
}

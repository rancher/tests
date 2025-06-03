//go:build (validation || extended || infra.any || cluster.any) && !sanity && !stress

package snapshot

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/etcdsnapshot"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SnapshotRestoreUpgradeStrategyTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *etcdsnapshot.Config
}

func (s *SnapshotRestoreUpgradeStrategyTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRestoreUpgradeStrategyTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.clustersConfig = new(etcdsnapshot.Config)
	config.LoadConfig(etcdsnapshot.ConfigurationFileKey, s.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *SnapshotRestoreUpgradeStrategyTestSuite) TestSnapshotRestoreUpgradeStrategy() {
	snapshotRestoreAll := &etcdsnapshot.Config{
		UpgradeKubernetesVersion:     "",
		SnapshotRestore:              "all",
		ControlPlaneConcurrencyValue: "15%",
		ControlPlaneUnavailableValue: "3",
		WorkerConcurrencyValue:       "20%",
		WorkerUnavailableValue:       "15%",
		RecurringRestores:            1,
	}

	tests := []struct {
		name         string
		clusterType  string
		etcdSnapshot *etcdsnapshot.Config
		client       *rancher.Client
	}{
		{"RKE1_Restore_Upgrade_Strategy", "rke1", snapshotRestoreAll, s.client},
		{"RKE2_Restore_Upgrade_Strategy", "rke2", snapshotRestoreAll, s.client},
		{"K3S_Restore_Upgrade_Strategy", "k3s", snapshotRestoreAll, s.client},
	}

	existingClusterType, err := actionsClusters.GetClusterType(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	for _, tt := range tests {
		s.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				s.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			err := etcdsnapshot.CreateAndValidateSnapshotRestore(tt.client, tt.client.RancherConfig.ClusterName, tt.etcdSnapshot, containerImage)
			require.NoError(s.T(), err)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestSnapshotRestoreUpgradeStrategyTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRestoreUpgradeStrategyTestSuite))
}

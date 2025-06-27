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

const (
	namespace      = "fleet-default"
	containerImage = "nginx"
)

type SnapshotRestoreTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *etcdsnapshot.Config
}

func (s *SnapshotRestoreTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRestoreTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.clustersConfig = new(etcdsnapshot.Config)
	config.LoadConfig(etcdsnapshot.ConfigurationFileKey, s.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *SnapshotRestoreTestSuite) TestSnapshotRestoreETCDOnly() {
	snapshotRestoreNone := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
	}

	tests := []struct {
		name         string
		clusterType  string
		etcdSnapshot *etcdsnapshot.Config
		client       *rancher.Client
	}{
		{"RKE1_Restore_ETCD", "rke1", snapshotRestoreNone, s.client},
		{"RKE2_Restore_ETCD", "rke2", snapshotRestoreNone, s.client},
		{"K3S_Restore_ETCD", "k3s", snapshotRestoreNone, s.client},
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
func TestSnapshotRestoreETCDOnlyTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRestoreTestSuite))
}

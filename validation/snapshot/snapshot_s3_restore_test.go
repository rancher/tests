//go:build (validation || extended || infra.any || cluster.any) && !sanity && !stress

package snapshot

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/etcdsnapshot"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type S3SnapshotRestoreTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *etcdsnapshot.Config
}

func (s *S3SnapshotRestoreTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *S3SnapshotRestoreTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.clustersConfig = new(etcdsnapshot.Config)
	config.LoadConfig(etcdsnapshot.ConfigurationFileKey, s.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *S3SnapshotRestoreTestSuite) TestS3SnapshotRestore() {
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
		{"RKE1_S3_Restore", "rke1", snapshotRestoreNone, s.client},
		{"RKE2_S3_Restore", "rke2", snapshotRestoreNone, s.client},
		{"K3S_S3_Restore", "k3s", snapshotRestoreNone, s.client},
	}

	existingClusterType, err := clusters.GetClusterType(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)

	for _, tt := range tests {
		s.Run(tt.name, func() {
			if tt.clusterType != existingClusterType {
				s.T().Skipf("Cluster type is not %s", tt.clusterType)
			}

			err := etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, s.client.RancherConfig.ClusterName, tt.etcdSnapshot, containerImage)
			require.NoError(s.T(), err)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestS3SnapshotTestSuite(t *testing.T) {
	suite.Run(t, new(S3SnapshotRestoreTestSuite))
}

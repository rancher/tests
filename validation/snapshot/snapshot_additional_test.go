//go:build validation

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

type SnapshotAdditionalTestsTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *etcdsnapshot.Config
}

func (s *SnapshotAdditionalTestsTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotAdditionalTestsTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.clustersConfig = new(etcdsnapshot.Config)
	config.LoadConfig(etcdsnapshot.ConfigurationFileKey, s.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *SnapshotAdditionalTestsTestSuite) TestSnapshotReplaceNodes() {
	controlPlaneSnapshotRestore := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
		ReplaceRoles: &etcdsnapshot.ReplaceRoles{
			ControlPlane: true,
		},
	}

	etcdSnapshotRestore := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
		ReplaceRoles: &etcdsnapshot.ReplaceRoles{
			Etcd: true,
		},
	}

	workerSnapshotRestore := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
		ReplaceRoles: &etcdsnapshot.ReplaceRoles{
			Worker: true,
		},
	}

	tests := []struct {
		name         string
		clusterType  string
		etcdSnapshot *etcdsnapshot.Config
		client       *rancher.Client
	}{
		{"RKE1_Replace_Control_Plane_Nodes", "rke1", controlPlaneSnapshotRestore, s.client},
		{"RKE1_Replace_ETCD_Nodes", "rke1", etcdSnapshotRestore, s.client},
		{"RKE1_Replace_Worker_Nodes", "rke1", workerSnapshotRestore, s.client},
		{"RKE2_Replace_Control_Plane_Nodes", "rke2", controlPlaneSnapshotRestore, s.client},
		{"RKE2_Replace_ETCD_Nodes", "rke2", etcdSnapshotRestore, s.client},
		{"RKE2_Replace_Worker_Nodes", "rke2", workerSnapshotRestore, s.client},
		{"K3S_Replace_Control_Plane_Nodes", "k3s", controlPlaneSnapshotRestore, s.client},
		{"K3S_Replace_ETCD_Nodes", "k3s", etcdSnapshotRestore, s.client},
		{"K3S_Replace_Worker_Nodes", "k3s", workerSnapshotRestore, s.client},
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

func (s *SnapshotAdditionalTestsTestSuite) TestSnapshotRecurringRestores() {
	snapshotRestoreFiveTimes := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        5,
	}

	tests := []struct {
		name         string
		clusterType  string
		etcdSnapshot *etcdsnapshot.Config
		client       *rancher.Client
	}{
		{"RKE1_Recurring_Restores", "rke1", snapshotRestoreFiveTimes, s.client},
		{"RKE2_Recurring_Restores", "rke2", snapshotRestoreFiveTimes, s.client},
		{"K3S_Recurring_Restores", "k3s", snapshotRestoreFiveTimes, s.client},
	}

	existingClusterType, err := actionsClusters.GetClusterType(s.client, s.client.RancherConfig.ClusterName)
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
func TestSnapshotAdditionalTestsTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotAdditionalTestsTestSuite))
}

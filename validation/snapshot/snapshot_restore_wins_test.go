//go:build validation

package snapshot

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/tests/actions/etcdsnapshot"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	windowsContainerImage = "mcr.microsoft.com/windows/servercore/iis"
)

type SnapshotRestoreWindowsTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	clustersConfig *etcdsnapshot.Config
}

func (s *SnapshotRestoreWindowsTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRestoreWindowsTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	s.clustersConfig = new(etcdsnapshot.Config)
	config.LoadConfig(etcdsnapshot.ConfigurationFileKey, s.clustersConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client
}

func (s *SnapshotRestoreWindowsTestSuite) TestSnapshotRestoreWindows() {
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
		etcdSnapshot *etcdsnapshot.Config
		client       *rancher.Client
	}{
		{"RKE2_Windows_Restore", snapshotRestoreAll, s.client},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := etcdsnapshot.CreateAndValidateSnapshotRestore(tt.client, tt.client.RancherConfig.ClusterName, tt.etcdSnapshot, windowsContainerImage)
			require.NoError(s.T(), err)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestSnapshotRestoreWindowsTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRestoreWindowsTestSuite))
}

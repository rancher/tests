//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SnapshotDualstackRestoreWindowsTestSuite struct {
	suite.Suite
	session                *session.Session
	client                 *rancher.Client
	cattleConfig           map[string]any
	rke2IPv4ClusterID      string
	rke2DualstackClusterID string
}

func (s *SnapshotDualstackRestoreWindowsTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotDualstackRestoreWindowsTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	rke2IPv4ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2IPv4ClusterConfig)

	rke2IPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	rke2DualstackClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2DualstackClusterConfig)

	rke2DualstackClusterConfig.IPv6Cluster = true
	rke2DualstackClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "dual",
	}

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, s.cattleConfig, awsEC2Configs)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
		provisioninginput.WindowsMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[3].MachinePoolConfig.Quantity = 1

	rke2IPv4ClusterConfig.MachinePools = nodeRolesStandard
	rke2DualstackClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 Windows cluster w/ipv4 stack preference")
	s.rke2IPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2IPv4ClusterConfig, awsEC2Configs, true, true)
	require.NoError(s.T(), err)

	logrus.Info("Provisioning RKE2 Windows cluster w/dual stack preference")
	s.rke2DualstackClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2DualstackClusterConfig, awsEC2Configs, true, true)
	require.NoError(s.T(), err)
}

func (s *SnapshotDualstackRestoreWindowsTestSuite) TestSnapshotDualstackRestoreWindows() {
	snapshotRestoreNone := &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
	}

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		clusterID    string
	}{
		{"RKE2_IPv4_Windows_Restore", snapshotRestoreNone, s.rke2IPv4ClusterID},
		{"RKE2_Dualstack_Windows_Restore", snapshotRestoreNone, s.rke2DualstackClusterID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			err := etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, cluster.Name, tt.etcdSnapshot, windowsContainerImage)
			require.NoError(s.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestSnapshotDualstackRestoreWindowsTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotDualstackRestoreWindowsTestSuite))
}

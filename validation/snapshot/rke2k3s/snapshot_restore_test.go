//go:build (validation || recurring || extended || infra.any || cluster.any || pit.weekly) && !sanity && !stress

package rke2k3s

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

const (
	containerImage        = "nginx"
	windowsContainerImage = "mcr.microsoft.com/windows/servercore/iis"
)

type SnapshotRestoreTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	rke2ClusterID string
	k3sClusterID  string
}

func (s *SnapshotRestoreTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRestoreTestSuite) SetupSuite() {
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

	rke2ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2ClusterConfig)

	k3sClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, k3sClusterConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, s.cattleConfig, awsEC2Configs)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	rke2ClusterConfig.MachinePools = nodeRolesStandard
	k3sClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster")
	s.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2ClusterConfig, awsEC2Configs, false, false)
	require.NoError(s.T(), err)

	logrus.Info("Provisioning K3S cluster")
	s.k3sClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sClusterConfig, awsEC2Configs, false, false)
	require.NoError(s.T(), err)
}

func snapshotRestoreConfigs() []*etcdsnapshot.Config {
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

func (s *SnapshotRestoreTestSuite) TestSnapshotRestore() {
	snapshotRestoreConfigRKE2 := snapshotRestoreConfigs()
	snapshotRestoreConfigK3s := snapshotRestoreConfigs()
	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		clusterID    string
	}{
		{"RKE2_Restore_ETCD", snapshotRestoreConfigRKE2[0], s.rke2ClusterID},
		{"RKE2_Restore_ETCD_K8sVersion", snapshotRestoreConfigRKE2[1], s.rke2ClusterID},
		{"RKE2_Restore_Upgrade_Strategy", snapshotRestoreConfigRKE2[2], s.rke2ClusterID},
		{"K3S_Restore_ETCD", snapshotRestoreConfigK3s[0], s.k3sClusterID},
		{"K3S_Restore_ETCD_K8sVersion", snapshotRestoreConfigK3s[1], s.k3sClusterID},
		{"K3S_Restore_Upgrade_Strategy", snapshotRestoreConfigK3s[2], s.k3sClusterID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			err := etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, cluster.Name, tt.etcdSnapshot, containerImage)
			require.NoError(s.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestSnapshotRestoreTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRestoreTestSuite))
}

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

var (
	snapshotRestoreNone = &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "none",
		RecurringRestores:        1,
	}

	snapshotRestoreK8sVersion = &etcdsnapshot.Config{
		UpgradeKubernetesVersion: "",
		SnapshotRestore:          "kubernetesVersion",
		RecurringRestores:        1,
	}

	snapshotRestoreAll = &etcdsnapshot.Config{
		UpgradeKubernetesVersion:     "",
		SnapshotRestore:              "all",
		ControlPlaneConcurrencyValue: "15%",
		WorkerConcurrencyValue:       "20%",
		RecurringRestores:            1,
	}
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

	standardUserClient, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

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

	s.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2ClusterConfig, awsEC2Configs, false, false)
	require.NoError(s.T(), err)

	s.k3sClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sClusterConfig, awsEC2Configs, false, false)
	require.NoError(s.T(), err)
}

func (s *SnapshotRestoreTestSuite) createAndVerifySnapshotRestoreByConfig(name string, etcdSnapshot *etcdsnapshot.Config, clusterID string) {
	cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
	require.NoError(s.T(), err)

	s.Run(name, func() {
		err := etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, cluster.Name, etcdSnapshot, containerImage)
		require.NoError(s.T(), err)
	})

	params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
	err = qase.UpdateSchemaParameters(name, params)
	if err != nil {
		logrus.Warningf("Failed to upload schema parameters %s", err)
	}
}

func (s *SnapshotRestoreTestSuite) TestSnapshotRestoreRKE2() {
	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		clusterID    string
	}{
		{"RKE2_Restore_ETCD", snapshotRestoreNone, s.rke2ClusterID},
		{"RKE2_Restore_ETCD_K8sVersion", snapshotRestoreK8sVersion, s.rke2ClusterID},
		{"RKE2_Restore_Upgrade_Strategy", snapshotRestoreAll, s.rke2ClusterID},
	}
	for _, tt := range tests {
		s.createAndVerifySnapshotRestoreByConfig(tt.name, tt.etcdSnapshot, tt.clusterID)
	}
}

func (s *SnapshotRestoreTestSuite) TestSnapshotRestoreK3S() {
	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		clusterID    string
	}{
		{"K3S_Restore_ETCD", snapshotRestoreNone, s.k3sClusterID},
		{"K3S_Restore_ETCD_K8sVersion", snapshotRestoreK8sVersion, s.k3sClusterID},
		{"K3S_Restore_Upgrade_Strategy", snapshotRestoreAll, s.k3sClusterID},
	}
	for _, tt := range tests {
		s.createAndVerifySnapshotRestoreByConfig(tt.name, tt.etcdSnapshot, tt.clusterID)
	}
}

func TestSnapshotRestoreTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRestoreTestSuite))
}

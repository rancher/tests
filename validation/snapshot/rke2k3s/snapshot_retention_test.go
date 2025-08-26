//go:build (validation || extended || infra.any || cluster.any || pit.weekly) && !sanity && !stress

package rke2k3s

import (
	"fmt"
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
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SnapshotRetentionTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	cattleConfig   map[string]any
	rke2ClusterID  string
	k3sClusterID   string
	snapshotConfig *SnapshotRetentionConfig
}

type SnapshotRetentionConfig struct {
	ClusterName       string `json:"clusterName" yaml:"clusterName"`
	SnapshotInterval  int    `json:"snapshotInterval" yaml:"snapshotInterval"`
	SnapshotRetention int    `json:"snapshotRetention" yaml:"snapshotRetention"`
}

func (s *SnapshotRetentionTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRetentionTestSuite) SetupSuite() {
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

	s.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2ClusterConfig, awsEC2Configs, true, false)
	require.NoError(s.T(), err)

	s.k3sClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sClusterConfig, awsEC2Configs, true, false)
	require.NoError(s.T(), err)
}

func (s *SnapshotRetentionTestSuite) TestAutomaticSnapshotRetention() {
	tests := []struct {
		testName                 string
		clusterID                string
		retentionLimit           int
		intervalBetweenSnapshots int
	}{
		{"RKE2_Retention_Limit", s.rke2ClusterID, 2, 1},
		{"K3S_Retention_Limit", s.k3sClusterID, 2, 1},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		clusterObject, clusterResponse, err := extClusters.GetProvisioningClusterByName(s.client, cluster.Name, "fleet-default")
		require.NoError(s.T(), err)

		clusterObject.Spec.RKEConfig.ETCD.SnapshotRetention = s.snapshotConfig.SnapshotRetention
		cronSchedule := fmt.Sprintf("%s%v%s", "*/", s.snapshotConfig.SnapshotInterval, " * * * *")
		clusterObject.Spec.RKEConfig.ETCD.SnapshotScheduleCron = cronSchedule

		_, err = s.client.Steve.SteveType(stevetypes.Provisioning).Update(clusterResponse, clusterObject)
		require.NoError(s.T(), err)

		s.Run(tt.testName, func() {
			err := etcdsnapshot.CreateSnapshotsUntilRetentionLimit(s.client, cluster.Name, tt.retentionLimit, tt.intervalBetweenSnapshots)
			require.NoError(s.T(), err)
		})
	}
}

func TestSnapshotRetentionTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRetentionTestSuite))
}

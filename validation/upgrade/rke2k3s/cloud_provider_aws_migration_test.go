//go:build validation

package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/rancher/tests/validation/upgrade"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MigrateCloudProviderSuite struct {
	suite.Suite
	session            *session.Session
	client             *rancher.Client
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
	clusterConfig      *clusters.ClusterConfig
	rke2ClusterID      string
}

func (u *MigrateCloudProviderSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *MigrateCloudProviderSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	client, err := rancher.NewClient("", u.session)
	require.NoError(u.T(), err)

	u.client = client

	u.standardUserClient, _, _, err = standard.CreateStandardUser(u.client)
	require.NoError(u.T(), err)

	u.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(u.T(), err)

	u.clusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.clusterConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, u.cattleConfig, awsEC2Configs)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	u.clusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster")
	u.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), u.standardUserClient, extClusters.RKE2ClusterType.String(), u.clusterConfig, awsEC2Configs, true, false)
	require.NoError(u.T(), err)
}

func (u *MigrateCloudProviderSuite) TestAWS() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2 AWS migration", u.rke2ClusterID},
	}

	for _, tt := range tests {
		cluster, err := u.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(u.T(), err)

		_, steveClusterObject, err := extClusters.GetProvisioningClusterByName(u.client, cluster.Name, namespaces.FleetDefault)
		require.NoError(u.T(), err)

		u.Run(tt.name, func() {
			upgrade.RKE2AWSCloudProviderMigration(u.T(), u.client, steveClusterObject)
		})
	}
}

func TestCloudProviderMigrationTestSuite(t *testing.T) {
	suite.Run(t, new(MigrateCloudProviderSuite))
}

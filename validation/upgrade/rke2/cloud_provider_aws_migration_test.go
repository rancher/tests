//go:build validation

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionsClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
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
	cluster            *v1.SteveAPIObject
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

	u.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	u.cattleConfig, err = defaults.LoadPackageDefaults(u.cattleConfig, "")
	require.NoError(u.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(u.T(), err)

	u.clusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, u.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		u.standardUserClient, _, _, err = standard.CreateStandardUser(u.client)
		require.NoError(u.T(), err)

		awsEC2Configs := new(ec2.AWSEC2Configs)
		operations.LoadObjectFromMap(ec2.ConfigurationFileKey, u.cattleConfig, awsEC2Configs)

		provider := provisioning.CreateProvider(u.clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(u.cattleConfig)

		logrus.Info("Provisioning RKE2 cluster")
		u.cluster, err = resources.ProvisionRKE2K3SCluster(u.T(), u.standardUserClient, defaults.RKE2, provider, *u.clusterConfig, machineConfigSpec, awsEC2Configs, true, false)
		require.NoError(u.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		u.cluster, err = u.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + rancherConfig.ClusterName)
		require.NoError(u.T(), err)
	}
}

func (u *MigrateCloudProviderSuite) TestAWS() {
	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"RKE2 AWS migration", u.cluster},
	}

	for _, tt := range tests {
		cluster, err := u.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.cluster.ID)
		require.NoError(u.T(), err)

		_, steveClusterObject, err := extensionsClusters.GetProvisioningClusterByName(u.client, cluster.Name, namespaces.FleetDefault)
		require.NoError(u.T(), err)

		u.Run(tt.name, func() {
			upgrade.RKE2AWSCloudProviderMigration(u.T(), u.client, steveClusterObject)
		})
	}
}

func TestCloudProviderMigrationTestSuite(t *testing.T) {
	suite.Run(t, new(MigrateCloudProviderSuite))
}

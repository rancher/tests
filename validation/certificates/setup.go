package certificates

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
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
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type certRotationTest struct {
	suite.Suite
	Session       *session.Session
	Client        *rancher.Client
	CattleConfig  map[string]any
	ClusterConfig *clusters.ClusterConfig
	rancherConfig *rancher.Config
	Cluster       *v1.SteveAPIObject
}

func Setup(t *testing.T, clusterType string) *certRotationTest {
	c := &certRotationTest{}

	testSession := session.NewSession()
	c.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	c.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(c.Client)
	require.NoError(t, err)

	c.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	c.CattleConfig, err = defaults.LoadPackageDefaults(c.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, c.CattleConfig, clusterConfig)

	c.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, c.CattleConfig, rancherConfig)

	c.rancherConfig = rancherConfig

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(c.CattleConfig)

		logrus.Infof("Provisioning %s cluster", clusterType)
		c.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		c.Cluster, err = c.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + c.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return c
}

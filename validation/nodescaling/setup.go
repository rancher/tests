package nodescaling

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

type nodeScalingTest struct {
	suite.Suite
	Client        *rancher.Client
	Session       *session.Session
	CattleConfig  map[string]any
	ClusterConfig *clusters.ClusterConfig
	rancherConfig *rancher.Config
	Cluster       *v1.SteveAPIObject
}

func Setup(t *testing.T, clusterType string) *nodeScalingTest {
	s := &nodeScalingTest{}

	testSession := session.NewSession()
	s.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	s.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.Client)
	require.NoError(t, err)

	s.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.CattleConfig, err = defaults.LoadPackageDefaults(s.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.CattleConfig, clusterConfig)

	s.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.CattleConfig, rancherConfig)

	s.rancherConfig = rancherConfig

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(s.CattleConfig)

		logrus.Infof("Provisioning %s cluster", clusterType)
		s.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		s.Cluster, err = s.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return s
}

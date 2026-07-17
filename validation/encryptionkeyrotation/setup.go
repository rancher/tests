package encryptionkeyrotation

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

type encryptionKeyRotationTest struct {
	suite.Suite
	Client        *rancher.Client
	Session       *session.Session
	CattleConfig  map[string]any
	ClusterConfig *clusters.ClusterConfig
	rancherConfig *rancher.Config
	Cluster       *v1.SteveAPIObject
}

func Setup(t *testing.T, clusterType string) *encryptionKeyRotationTest {
	e := &encryptionKeyRotationTest{}

	testSession := session.NewSession()
	e.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	e.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(e.Client)
	require.NoError(t, err)

	e.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	e.CattleConfig, err = defaults.LoadPackageDefaults(e.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, e.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, e.CattleConfig, clusterConfig)

	e.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, e.CattleConfig, rancherConfig)

	e.rancherConfig = rancherConfig

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(e.CattleConfig)

		logrus.Infof("Provisioning %s cluster", clusterType)
		e.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		e.Cluster, err = e.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + e.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return e
}

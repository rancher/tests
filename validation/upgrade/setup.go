package upgrade

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
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
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type upgradeTest struct {
	suite.Suite
	Client        *rancher.Client
	Session       *session.Session
	CattleConfig  map[string]any
	ClusterConfig *clusters.ClusterConfig
	rancherConfig *rancher.Config
	Cluster       *v1.SteveAPIObject
}

func Setup(t *testing.T, clusterType string, isWindows bool) *upgradeTest {
	u := &upgradeTest{}

	testSession := session.NewSession()
	u.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	u.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(u.Client)
	require.NoError(t, err)

	u.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	u.CattleConfig, err = defaults.LoadPackageDefaults(u.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.CattleConfig, clusterConfig)

	u.ClusterConfig = clusterConfig

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, u.CattleConfig, awsEC2Configs)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, u.CattleConfig, rancherConfig)

	u.rancherConfig = rancherConfig

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(u.CattleConfig)

		if isWindows {
			nodeRolesStandard := []provisioninginput.MachinePools{
				provisioninginput.EtcdMachinePool,
				provisioninginput.ControlPlaneMachinePool,
				provisioninginput.WorkerMachinePool,
				provisioninginput.WindowsMachinePool,
			}

			nodeRolesStandard[0].MachinePoolConfig.Quantity = 1
			nodeRolesStandard[1].MachinePoolConfig.Quantity = 1
			nodeRolesStandard[2].MachinePoolConfig.Quantity = 1
			nodeRolesStandard[3].MachinePoolConfig.Quantity = 1

			logrus.Info("Provisioning RKE2 windows cluster")
			u.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, defaults.RKE2, provider, *clusterConfig, machineConfigSpec, awsEC2Configs, false, true)
			require.NoError(t, err)
		} else {
			logrus.Infof("Provisioning %s cluster", clusterType)
			u.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, clusterType, provider, *clusterConfig, machineConfigSpec, nil, false, false)
			require.NoError(t, err)
		}
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		u.Cluster, err = u.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + u.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return u
}

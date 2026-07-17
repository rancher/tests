package deleting

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
	"github.com/rancher/tests/actions/provisioninginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type deleteTest struct {
	suite.Suite
	Client        *rancher.Client
	Session       *session.Session
	CattleConfig  map[string]any
	ClusterConfig *clusters.ClusterConfig
	rancherConfig *rancher.Config
	Cluster       *v1.SteveAPIObject
}

func Setup(t *testing.T, clusterType string, defaultNodeRoles bool) *deleteTest {
	d := &deleteTest{}

	testSession := session.NewSession()
	d.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	d.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(d.Client)
	require.NoError(t, err)

	d.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	d.CattleConfig, err = defaults.LoadPackageDefaults(d.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, d.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.CattleConfig, clusterConfig)

	d.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, d.CattleConfig, rancherConfig)

	d.rancherConfig = rancherConfig

	if rancherConfig.ClusterName == "" {
		if !defaultNodeRoles {
			nodeRolesStandard := []provisioninginput.MachinePools{
				provisioninginput.EtcdMachinePool,
				provisioninginput.ControlPlaneMachinePool,
				provisioninginput.WorkerMachinePool,
			}

			nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
			nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
			nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

			clusterConfig.MachinePools = nodeRolesStandard
		}

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(d.CattleConfig)

		logrus.Infof("Provisioning %s cluster", clusterType)
		d.Cluster, err = resources.ProvisionRKE2K3SCluster(t, standardUserClient, clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		d.Cluster, err = d.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + d.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return d
}

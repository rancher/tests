package ipv6

import (
	"os"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/rancher/tests/validation/upgrade"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	upstream "go.qase.io/client"
)

type UpgradeIPv6KubernetesTestSuite struct {
	suite.Suite
	session           *session.Session
	client            *rancher.Client
	cattleConfig      map[string]any
	rke2ClusterConfig *clusters.ClusterConfig
	rke2ClusterID     string
}

func (u *UpgradeIPv6KubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeIPv6KubernetesTestSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	client, err := rancher.NewClient("", u.session)
	require.NoError(u.T(), err)

	u.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(u.client)
	require.NoError(u.T(), err)

	u.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(u.T(), err)

	u.rke2ClusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.rke2ClusterConfig)

	u.rke2ClusterConfig.IPv6Cluster = true

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

	u.rke2ClusterConfig.MachinePools = nodeRolesStandard

	for i := range u.rke2ClusterConfig.MachinePools {
		u.rke2ClusterConfig.MachinePools[i].SpecifyCustomPublicIP = true
		u.rke2ClusterConfig.MachinePools[i].SpecifyCustomPrivateIP = true
	}

	logrus.Info("Provisioning RKE2 cluster")
	u.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.RKE2ClusterType.String(), u.rke2ClusterConfig, awsEC2Configs, false, true)
	require.NoError(u.T(), err)
}

func (u *UpgradeIPv6KubernetesTestSuite) TestUpgradeIPv6Kubernetes() {
	tests := []struct {
		name          string
		clusterID     string
		clusterConfig *clusters.ClusterConfig
		clusterType   string
	}{
		{"Upgrading_RKE2_IPv6_cluster", u.rke2ClusterID, u.rke2ClusterConfig, extClusters.RKE2ClusterType.String()},
	}

	for _, tt := range tests {
		version, err := kubernetesversions.Default(u.client, tt.clusterType, nil)
		require.NoError(u.T(), err)

		clusterResp, err := u.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(u.T(), err)

		updatedCluster := new(provv1.Cluster)
		err = v1.ConvertToK8sType(clusterResp, &updatedCluster)
		require.NoError(u.T(), err)

		tt.clusterConfig.KubernetesVersion = version[0]

		u.Run(tt.name, func() {
			upgrade.DownstreamCluster(&u.Suite, tt.name, u.client, clusterResp.Name, tt.clusterConfig, tt.clusterID, tt.clusterConfig.KubernetesVersion, false)
		})

		upgradedK8sParam := upstream.Params{Title: "UpgradedK8sVersion", Values: []string{tt.clusterConfig.KubernetesVersion}}
		params := provisioning.GetProvisioningSchemaParams(u.client, u.cattleConfig)
		params = append(params, upgradedK8sParam)

		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestUpgradeIPv6KubernetesTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeIPv6KubernetesTestSuite))
}

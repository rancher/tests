//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
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
	upstream "go.qase.io/qase-api-client"
)

type UpgradeDualstackKubernetesTestSuite struct {
	suite.Suite
	session                    *session.Session
	client                     *rancher.Client
	cattleConfig               map[string]any
	rke2IPv4ClusterConfig      *clusters.ClusterConfig
	rke2DualstackClusterConfig *clusters.ClusterConfig
	k3sIPv4ClusterConfig       *clusters.ClusterConfig
	k3sDualstackClusterConfig  *clusters.ClusterConfig
	rke2IPv4ClusterID          string
	rke2DualstackClusterID     string
	k3sIPv4ClusterID           string
	k3sDualstackClusterID      string
}

func (u *UpgradeDualstackKubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeDualstackKubernetesTestSuite) SetupSuite() {
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

	u.rke2IPv4ClusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.rke2IPv4ClusterConfig)

	u.rke2IPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	u.rke2DualstackClusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.rke2DualstackClusterConfig)

	u.rke2DualstackClusterConfig.IPv6Cluster = true
	u.rke2DualstackClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "dual",
	}

	u.k3sIPv4ClusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.k3sIPv4ClusterConfig)

	u.k3sIPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	u.k3sDualstackClusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.k3sDualstackClusterConfig)

	u.k3sDualstackClusterConfig.IPv6Cluster = true
	u.k3sDualstackClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "dual",
	}

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	u.rke2IPv4ClusterConfig.MachinePools = nodeRolesStandard
	u.rke2DualstackClusterConfig.MachinePools = nodeRolesStandard
	u.k3sIPv4ClusterConfig.MachinePools = nodeRolesStandard
	u.k3sDualstackClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster w/ipv4 stack preference")
	u.rke2IPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.RKE2ClusterType.String(), u.rke2IPv4ClusterConfig, nil, false, false)
	require.NoError(u.T(), err)

	logrus.Info("Provisioning RKE2 cluster w/dual stack preference")
	u.rke2DualstackClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.RKE2ClusterType.String(), u.rke2DualstackClusterConfig, nil, false, false)
	require.NoError(u.T(), err)

	logrus.Info("Provisioning K3S cluster w/ipv4 stack preference")
	u.k3sIPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.K3SClusterType.String(), u.k3sIPv4ClusterConfig, nil, false, false)
	require.NoError(u.T(), err)

	logrus.Info("Provisioning K3S cluster w/dual stack preference")
	u.k3sDualstackClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.K3SClusterType.String(), u.k3sDualstackClusterConfig, nil, false, false)
	require.NoError(u.T(), err)
}

func (u *UpgradeDualstackKubernetesTestSuite) TestUpgradeDualstackKubernetes() {
	tests := []struct {
		name          string
		clusterID     string
		clusterConfig *clusters.ClusterConfig
		clusterType   string
	}{
		{"Upgrading_RKE2_IPv4_cluster", u.rke2IPv4ClusterID, u.rke2IPv4ClusterConfig, extClusters.RKE2ClusterType.String()},
		{"Upgrading_RKE2_Dualstack_cluster", u.rke2DualstackClusterID, u.rke2DualstackClusterConfig, extClusters.RKE2ClusterType.String()},
		{"Upgrading_K3S_IPv4_cluster", u.k3sIPv4ClusterID, u.k3sIPv4ClusterConfig, extClusters.K3SClusterType.String()},
		{"Upgrading_K3S_Dualstack_cluster", u.k3sDualstackClusterID, u.k3sDualstackClusterConfig, extClusters.K3SClusterType.String()},
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

		upgradedK8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "UpgradedK8sVersion", Values: []string{tt.clusterConfig.KubernetesVersion}}}
		params := provisioning.GetProvisioningSchemaParams(u.client, u.cattleConfig)
		params = append(params, upgradedK8sParam)

		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestUpgradeDualstackKubernetesTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeDualstackKubernetesTestSuite))
}

//go:build validation || recurring || pit.weekly

package rke2k3s

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
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/upgradeinput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/rancher/tests/validation/upgrade"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	upstream "go.qase.io/client"
)

type UpgradeWindowsKubernetesTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	rke2ClusterID string
	clusters      []upgradeinput.Cluster
}

func (u *UpgradeWindowsKubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeWindowsKubernetesTestSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	client, err := rancher.NewClient("", u.session)
	require.NoError(u.T(), err)

	u.client = client

	standardUserClient, err := standard.CreateStandardUser(u.client)
	require.NoError(u.T(), err)

	u.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	u.clusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.clusterConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, u.cattleConfig, awsEC2Configs)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
		provisioninginput.WindowsMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[3].MachinePoolConfig.Quantity = 1

	u.clusterConfig.MachinePools = nodeRolesStandard

	u.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.RKE2ClusterType.String(), u.clusterConfig, awsEC2Configs, true, true)
	require.NoError(u.T(), err)

	clusters, err := upgradeinput.LoadUpgradeKubernetesConfig(client)
	require.NoError(u.T(), err)

	u.clusters = clusters
}

func (u *UpgradeWindowsKubernetesTestSuite) TestUpgradeWindowsKubernetes() {
	tests := []struct {
		name          string
		clusterID     string
		clusterConfig *clusters.ClusterConfig
		clusterType   string
	}{
		{"Upgrading_RKE2_Windows_cluster", u.rke2ClusterID, u.clusterConfig, extClusters.RKE2ClusterType.String()},
	}

	var params []upstream.Params

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

		k8sParam := upstream.Params{Title: "K8sVersion", Values: []string{updatedCluster.Spec.KubernetesVersion}}
		upgradedK8sParam := upstream.Params{Title: "UpgradedK8sVersion", Values: []string{tt.clusterConfig.KubernetesVersion}}

		params = append(params, k8sParam, upgradedK8sParam)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestWindowsKubernetesUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeWindowsKubernetesTestSuite))
}

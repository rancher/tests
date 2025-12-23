//go:build validation || recurring

package ipv6

import (
	"os"
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
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
)

type UpgradeIPv6KubernetesTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	rke2Cluster   *v1.SteveAPIObject
	k3sCluster    *v1.SteveAPIObject
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

	u.cattleConfig, err = defaults.LoadPackageDefaults(u.cattleConfig, "")
	require.NoError(u.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(u.T(), err)

	u.clusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.clusterConfig)

	provider := provisioning.CreateProvider(u.clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(u.cattleConfig)

	logrus.Info("Provisioning RKE2 cluster")
	u.rke2Cluster, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *u.clusterConfig, machineConfigSpec, nil, false, false)
	require.NoError(u.T(), err)

	if u.clusterConfig.Advanced == nil {
		u.clusterConfig.Advanced = &provisioninginput.Advanced{}
	}

	if u.clusterConfig.Advanced.MachineGlobalConfig == nil {
		u.clusterConfig.Advanced.MachineGlobalConfig = &rkev1.GenericMap{
			Data: map[string]any{},
		}
	}

	u.clusterConfig.Advanced.MachineGlobalConfig.Data["flannel-ipv6-masq"] = true

	logrus.Info("Provisioning K3s cluster")
	u.k3sCluster, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, extClusters.K3SClusterType.String(), provider, *u.clusterConfig, machineConfigSpec, nil, false, false)
	require.NoError(u.T(), err)
}

func (u *UpgradeIPv6KubernetesTestSuite) TestUpgradeIPv6Kubernetes() {
	tests := []struct {
		name          string
		clusterID     string
		clusterConfig *clusters.ClusterConfig
		clusterType   string
	}{
		{"Upgrading_RKE2_IPv6_cluster", u.rke2Cluster.ID, u.clusterConfig, extClusters.RKE2ClusterType.String()},
		{"Upgrading_K3S_IPv6_cluster", u.k3sCluster.ID, u.clusterConfig, extClusters.K3SClusterType.String()},
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

func TestUpgradeIPv6KubernetesTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeIPv6KubernetesTestSuite))
}

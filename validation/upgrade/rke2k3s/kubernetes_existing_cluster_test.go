//go:build validation

package rke2k3s

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
	"github.com/rancher/tests/validation/upgrade"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type UpgradeKubernetesExistingClusterTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	clusterObject *v1.SteveAPIObject
}

func (u *UpgradeKubernetesExistingClusterTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeKubernetesExistingClusterTestSuite) SetupSuite() {
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

	u.clusterObject, err = client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + u.client.RancherConfig.ClusterName)
	require.NoError(u.T(), err)
}

func (u *UpgradeKubernetesExistingClusterTestSuite) TestUpgradeKubernetesExistingCluster() {
	tests := []struct {
		name          string
		clusterID     string
		clusterConfig *clusters.ClusterConfig
		clusterType   string
	}{
		{"Upgrading_existing_cluster", u.clusterObject.ID, u.clusterConfig, extClusters.RKE2ClusterType.String()},
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
	}
}

func TestKubernetesExistingClusterUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeKubernetesExistingClusterTestSuite))
}

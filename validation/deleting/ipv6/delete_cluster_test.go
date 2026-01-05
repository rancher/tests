//go:build validation || recurring

package ipv6

import (
	"os"
	"testing"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
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
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DeleteIPv6ClusterTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	rke2Cluster  *v1.SteveAPIObject
	k3sCluster   *v1.SteveAPIObject
}

func (d *DeleteIPv6ClusterTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteIPv6ClusterTestSuite) SetupSuite() {
	testSession := session.NewSession()
	d.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(d.T(), err)

	d.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(d.client)
	require.NoError(d.T(), err)

	d.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	d.cattleConfig, err = defaults.LoadPackageDefaults(d.cattleConfig, "")
	require.NoError(d.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, d.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(d.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, clusterConfig)

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(d.cattleConfig)

	logrus.Info("Provisioning RKE2 cluster")
	d.rke2Cluster, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(d.T(), err)

	if clusterConfig.Advanced == nil {
		clusterConfig.Advanced = &provisioninginput.Advanced{}
	}

	if clusterConfig.Advanced.MachineGlobalConfig == nil {
		clusterConfig.Advanced.MachineGlobalConfig = &rkev1.GenericMap{
			Data: map[string]any{},
		}
	}

	clusterConfig.Advanced.MachineGlobalConfig.Data["flannel-ipv6-masq"] = true

	logrus.Info("Provisioning K3s cluster")
	d.k3sCluster, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.K3SClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(d.T(), err)
}

func (d *DeleteIPv6ClusterTestSuite) TestDeletingIPv6Cluster() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2_Delete_IPv6_Cluster", d.rke2Cluster.ID},
		{"K3S_Delete_IPv6_Cluster", d.k3sCluster.ID},
	}

	for _, tt := range tests {
		d.Run(tt.name, func() {
			logrus.Infof("Deleting cluster (%s)", tt.clusterID)
			extClusters.DeleteK3SRKE2Cluster(d.client, tt.clusterID)

			logrus.Infof("Verifying cluster (%s) deletion", tt.clusterID)
			provisioning.VerifyDeleteRKE2K3SCluster(d.T(), d.client, tt.clusterID)
		})

		params := provisioning.GetProvisioningSchemaParams(d.client, d.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestDeleteIPv6ClusterTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteIPv6ClusterTestSuite))
}

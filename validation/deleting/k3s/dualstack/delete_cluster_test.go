//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DeleteDualstackClusterTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
}

func (d *DeleteDualstackClusterTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteDualstackClusterTestSuite) SetupSuite() {
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

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, d.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(d.cattleConfig)

		logrus.Info("Provisioning K3S cluster")
		d.cluster, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, defaults.K3S, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(d.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		d.cluster, err = d.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + rancherConfig.ClusterName)
		require.NoError(d.T(), err)
	}
}

func (d *DeleteDualstackClusterTestSuite) TestDeletingDualstackCluster() {
	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"K3S_Delete_Dualstack_Cluster", d.cluster},
	}

	for _, tt := range tests {
		d.Run(tt.name, func() {
			logrus.Infof("Deleting cluster (%s)", tt.cluster.Name)
			extClusters.DeleteK3SRKE2Cluster(d.client, tt.cluster.ID)

			logrus.Infof("Verifying cluster (%s) deletion", tt.cluster.ID)
			provisioning.VerifyDeleteRKE2K3SCluster(d.T(), d.client, tt.cluster.ID)
		})

		params := provisioning.GetProvisioningSchemaParams(d.client, d.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestDeleteDualstackClusterTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteDualstackClusterTestSuite))
}

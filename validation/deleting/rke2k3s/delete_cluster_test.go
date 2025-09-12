//go:build (infra.rke2k3s || validation || recurring) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !stress && !sanity && !extended

package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
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

type DeleteClusterTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	cattleConfig  map[string]any
	rke2ClusterID string
	k3sClusterID  string
}

func (d *DeleteClusterTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteClusterTestSuite) SetupSuite() {
	testSession := session.NewSession()
	d.session = testSession

	client, err := rancher.NewClient("", d.session)
	require.NoError(d.T(), err)

	d.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(d.client)
	require.NoError(d.T(), err)

	d.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, d.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(d.T(), err)

	rke2ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, rke2ClusterConfig)

	k3sClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, k3sClusterConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, d.cattleConfig, awsEC2Configs)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	rke2ClusterConfig.MachinePools = nodeRolesStandard
	k3sClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster")
	d.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2ClusterConfig, awsEC2Configs, true, false)
	require.NoError(d.T(), err)

	logrus.Info("Provisioning K3S cluster")
	d.k3sClusterID, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sClusterConfig, awsEC2Configs, true, false)
	require.NoError(d.T(), err)
}

func (d *DeleteClusterTestSuite) TestDeletingCluster() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2_Delete_Cluster", d.rke2ClusterID},
		{"K3S_Delete_Cluster", d.k3sClusterID},
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

func TestDeleteClusterTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteClusterTestSuite))
}

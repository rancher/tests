package ipv6

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/rancher/tests/validation/nodescaling"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CustomIPv6ClusterNodeScalingTestSuite struct {
	suite.Suite
	client            *rancher.Client
	session           *session.Session
	rke2ClusterConfig *clusters.ClusterConfig
	scalingConfig     *scalinginput.Config
	cattleConfig      map[string]any
	rke2ClusterID     string
}

func (s *CustomIPv6ClusterNodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *CustomIPv6ClusterNodeScalingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	s.rke2ClusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, s.rke2ClusterConfig)

	s.rke2ClusterConfig.IPv6Cluster = true

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, s.cattleConfig, awsEC2Configs)

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	s.rke2ClusterConfig.MachinePools = nodeRolesStandard

	for i := range s.rke2ClusterConfig.MachinePools {
		s.rke2ClusterConfig.MachinePools[i].SpecifyCustomPublicIP = true
		s.rke2ClusterConfig.MachinePools[i].SpecifyCustomPrivateIP = true
	}

	logrus.Info("Provisioning RKE2 cluster")
	s.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), s.rke2ClusterConfig, awsEC2Configs, true, true)
	require.NoError(s.T(), err)
}

func (s *CustomIPv6ClusterNodeScalingTestSuite) TestScalingCustomIPv6ClusterNodes() {
	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd:     true,
		Quantity: 1,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesEtcdControlPlane := machinepools.NodeRoles{
		Etcd:         true,
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker:   true,
		Quantity: 1,
	}

	tests := []struct {
		name          string
		nodeRoles     machinepools.NodeRoles
		clusterID     string
		clusterConfig *clusters.ClusterConfig
	}{
		{"RKE2_IPv6_Custom_Scale_Control_Plane", nodeRolesControlPlane, s.rke2ClusterID, s.rke2ClusterConfig},
		{"RKE2_IPv6_Custom_Scale_ETCD", nodeRolesEtcd, s.rke2ClusterID, s.rke2ClusterConfig},
		{"RKE2_IPv6_Custom_Scale_Control_Plane_ETCD", nodeRolesEtcdControlPlane, s.rke2ClusterID, s.rke2ClusterConfig},
		{"RKE2_IPv6_Custom_Scale_Worker", nodeRolesWorker, s.rke2ClusterID, s.rke2ClusterConfig},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, s.cattleConfig, awsEC2Configs)
			nodescaling.ScalingRKE2K3SCustomClusterPools(s.T(), s.client, tt.clusterID, s.scalingConfig.NodeProvider, tt.nodeRoles, awsEC2Configs, tt.clusterConfig)

			logrus.Infof("Verifying cluster (%s)", cluster.Name)
			provisioning.VerifyCluster(s.T(), s.client, cluster)
		})

		params := provisioning.GetCustomSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestCustomIPv6ClusterNodeScalingTestSuite(t *testing.T) {
	suite.Run(t, new(CustomIPv6ClusterNodeScalingTestSuite))
}

//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
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
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/nodescaling"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeScalingDualstackTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	scalingConfig *scalinginput.Config
	rke2Cluster   *v1.SteveAPIObject
	k3sCluster    *v1.SteveAPIObject
}

func (s *NodeScalingDualstackTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *NodeScalingDualstackTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.cattleConfig, err = defaults.LoadPackageDefaults(s.cattleConfig, "")
	require.NoError(s.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	s.clusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, s.clusterConfig)

	provider := provisioning.CreateProvider(s.clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	logrus.Info("Provisioning RKE2 cluster")
	s.rke2Cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *s.clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(s.T(), err)

	logrus.Info("Provisioning K3S cluster")
	s.k3sCluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), provider, *s.clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(s.T(), err)
}

func (s *NodeScalingDualstackTestSuite) TestScalingDualstackNodePools() {
	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd:     true,
		Quantity: 1,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
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
		{"RKE2_Dualstack_Scale_Control_Plane", nodeRolesControlPlane, s.rke2Cluster.ID, s.clusterConfig},
		{"RKE2_Dualstack_Scale_ETCD", nodeRolesEtcd, s.rke2Cluster.ID, s.clusterConfig},
		{"RKE2_Dualstack_Scale_Worker", nodeRolesWorker, s.rke2Cluster.ID, s.clusterConfig},
		{"K3S_Dualstack_Scale_Control_Plane", nodeRolesControlPlane, s.k3sCluster.ID, s.clusterConfig},
		{"K3S_Dualstack_Scale_ETCD", nodeRolesEtcd, s.k3sCluster.ID, s.clusterConfig},
		{"K3S_Dualstack_Scale_Worker", nodeRolesWorker, s.k3sCluster.ID, s.clusterConfig},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, s.cattleConfig, awsEC2Configs)
			nodescaling.ScalingRKE2K3SCustomClusterPools(s.T(), s.client, tt.clusterID, s.scalingConfig.NodeProvider, tt.nodeRoles, awsEC2Configs, tt.clusterConfig)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(s.client, cluster)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, cluster)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(s.client, cluster)
			require.NoError(s.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestNodeScalingDualstackTestSuite(t *testing.T) {
	suite.Run(t, new(NodeScalingDualstackTestSuite))
}

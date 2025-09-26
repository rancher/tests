//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

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
	"github.com/rancher/tests/validation/nodescaling"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeScalingDualstackTestSuite struct {
	suite.Suite
	client                 *rancher.Client
	session                *session.Session
	cattleConfig           map[string]any
	rke2ClusterConfig      *clusters.ClusterConfig
	rke2IPv4ClusterID      string
	rke2DualstackClusterID string
	k3sIPv4ClusterID       string
	k3sDualstackClusterID  string
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

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	rke2IPv4ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2IPv4ClusterConfig)

	rke2IPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	rke2DualstackClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2DualstackClusterConfig)

	rke2DualstackClusterConfig.IPv6Cluster = true
	rke2DualstackClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "dual",
	}

	k3sIPv4ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, k3sIPv4ClusterConfig)

	k3sIPv4ClusterConfig.Networking = &provisioninginput.Networking{
		StackPreference: "ipv4",
	}

	k3sDualstackClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, k3sDualstackClusterConfig)

	k3sDualstackClusterConfig.IPv6Cluster = true
	k3sDualstackClusterConfig.Networking = &provisioninginput.Networking{
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

	rke2IPv4ClusterConfig.MachinePools = nodeRolesStandard
	rke2DualstackClusterConfig.MachinePools = nodeRolesStandard
	k3sIPv4ClusterConfig.MachinePools = nodeRolesStandard
	k3sDualstackClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster w/ipv4 stack preference")
	s.rke2IPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2IPv4ClusterConfig, nil, true, false)
	require.NoError(s.T(), err)

	logrus.Info("Provisioning RKE2 cluster w/dual stack preference")
	s.rke2DualstackClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2DualstackClusterConfig, nil, true, false)
	require.NoError(s.T(), err)

	logrus.Info("Provisioning K3S cluster w/ipv4 stack preference")
	s.k3sIPv4ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sIPv4ClusterConfig, nil, true, false)
	require.NoError(s.T(), err)

	logrus.Info("Provisioning K3S cluster w/dual stack preference")
	s.k3sDualstackClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), k3sDualstackClusterConfig, nil, true, false)
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

	nodeRolesWindows := machinepools.NodeRoles{
		Windows:  true,
		Quantity: 1,
	}

	tests := []struct {
		name      string
		nodeRoles machinepools.NodeRoles
		clusterID string
		isWindows bool
	}{
		{"RKE2_IPv4_Node_Driver_Scale_Control_Plane", nodeRolesControlPlane, s.rke2IPv4ClusterID, false},
		{"RKE2_IPv4_Node_Driver_Scale_ETCD", nodeRolesEtcd, s.rke2IPv4ClusterID, false},
		{"RKE2_IPv4_Node_Driver_Scale_Worker", nodeRolesWorker, s.rke2IPv4ClusterID, false},
		{"RKE2_IPv4_Node_Driver_Scale_Windows", nodeRolesWindows, s.rke2IPv4ClusterID, true},
		{"RKE2_Dualstack_Node_Driver_Scale_Control_Plane", nodeRolesControlPlane, s.rke2DualstackClusterID, false},
		{"RKE2_Dualstack_Node_Driver_Scale_ETCD", nodeRolesEtcd, s.rke2DualstackClusterID, false},
		{"RKE2_Dualstack_Node_Driver_Scale_Worker", nodeRolesWorker, s.rke2DualstackClusterID, false},
		{"RKE2_Dualstack_Node_Driver_Scale_Windows", nodeRolesWindows, s.rke2DualstackClusterID, true},
		{"K3S_IPv4_Node_Driver_Scale_Control_Plane", nodeRolesControlPlane, s.k3sIPv4ClusterID, false},
		{"K3S_IPv4_Node_Driver_Scale_ETCD", nodeRolesEtcd, s.k3sIPv4ClusterID, false},
		{"K3S_IPv4_Node_Driver_Scale_Worker", nodeRolesWorker, s.k3sIPv4ClusterID, false},
		{"K3S_Dualstack_Node_Driver_Scale_Control_Plane", nodeRolesControlPlane, s.k3sDualstackClusterID, false},
		{"K3S_Dualstack_Node_Driver_Scale_ETCD", nodeRolesEtcd, s.k3sDualstackClusterID, false},
		{"K3S_Dualstack_Node_Driver_Scale_Worker", nodeRolesWorker, s.k3sDualstackClusterID, false},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			if s.rke2ClusterConfig.Provider != "vsphere" && tt.isWindows {
				s.T().Skip("Windows test requires access to vSphere")
			}

			nodescaling.ScalingRKE2K3SNodePools(s.T(), s.client, tt.clusterID, tt.nodeRoles)

			logrus.Infof("Verifying cluster (%s)", cluster.Name)
			provisioning.VerifyCluster(s.T(), s.client, cluster)
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

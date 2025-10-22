//go:build validation || recurring

package ipv6

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
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/nodescaling"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeScalingIPv6TestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	cattleConfig  map[string]any
	rke2ClusterID string
}

func (s *NodeScalingIPv6TestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *NodeScalingIPv6TestSuite) SetupSuite() {
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

	rke2ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2ClusterConfig)

	rke2ClusterConfig.Networking = &provisioninginput.Networking{
		ClusterCIDR:     rke2ClusterConfig.Networking.ClusterCIDR,
		ServiceCIDR:     rke2ClusterConfig.Networking.ServiceCIDR,
		StackPreference: rke2ClusterConfig.Networking.StackPreference,
	}

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	rke2ClusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 cluster")
	s.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), rke2ClusterConfig, nil, true, false)
	require.NoError(s.T(), err)
}

func (s *NodeScalingIPv6TestSuite) TestScalingIPv6NodePools() {
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
		name      string
		nodeRoles machinepools.NodeRoles
		clusterID string
	}{
		{"RKE2_IPv6_Scale_Control_Plane", nodeRolesControlPlane, s.rke2ClusterID},
		{"RKE2_IPv6_Scale_ETCD", nodeRolesEtcd, s.rke2ClusterID},
		{"RKE2_IPv6_Scale_Worker", nodeRolesWorker, s.rke2ClusterID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			nodescaling.ScalingRKE2K3SNodePools(s.T(), s.client, tt.clusterID, tt.nodeRoles)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(s.T(), s.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(s.T(), s.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestNodeScalingIPv6TestSuite(t *testing.T) {
	suite.Run(t, new(NodeScalingIPv6TestSuite))
}

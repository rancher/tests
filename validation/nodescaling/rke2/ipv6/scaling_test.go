//go:build validation || recurring

package ipv6

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
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
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"

	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeScalingIPv6TestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
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

	s.cattleConfig, err = defaults.LoadPackageDefaults(s.cattleConfig, "")
	require.NoError(s.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

		logrus.Info("Provisioning RKE2 cluster")
		s.cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, defaults.RKE2, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(s.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		s.cluster, err = s.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + rancherConfig.ClusterName)
		require.NoError(s.T(), err)
	}
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
		name          string
		nodeRoles     machinepools.NodeRoles
		scaleQuantity int32
		cluster       *v1.SteveAPIObject
	}{
		{"RKE2_IPv6_Scale_Control_Plane", nodeRolesControlPlane, 1, s.cluster},
		{"RKE2_IPv6_Scale_ETCD", nodeRolesEtcd, 1, s.cluster},
		{"RKE2_IPv6_Scale_Worker", nodeRolesWorker, 1, s.cluster},
	}

	for _, tt := range tests {
		var err error
		s.Run(tt.name, func() {
			tt.nodeRoles.Quantity = tt.scaleQuantity
			logrus.Infof("Scaling up the node pool (%s)", tt.cluster.Name)
			tt.cluster, err = machinepools.ScaleMachinePool(s.client, tt.cluster, tt.nodeRoles)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(s.client, tt.cluster)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, tt.cluster)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(s.client, tt.cluster)
			require.NoError(s.T(), err)

			tt.nodeRoles.Quantity = -tt.scaleQuantity
			logrus.Infof("Scaling down the node pool (%s)", tt.cluster.Name)
			_, err = machinepools.ScaleMachinePool(s.client, tt.cluster, tt.nodeRoles)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(s.client, tt.cluster)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, tt.cluster)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(s.client, tt.cluster)
			require.NoError(s.T(), err)
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

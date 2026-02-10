//go:build (validation || infra.rke2k3s || recurring || cluster.custom || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !cluster.any && !cluster.nodedriver && !sanity && !extended

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
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
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeScalingTestSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	scalingConfig *scalinginput.Config
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	cluster       *v1.SteveAPIObject
}

func (s *NodeScalingTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *NodeScalingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
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

	s.scalingConfig = new(scalinginput.Config)
	config.LoadConfig(scalinginput.ConfigurationFileKey, s.scalingConfig)

	provider := provisioning.CreateProvider(s.clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

	logrus.Info("Provisioning RKE2 cluster")
	s.cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, defaults.RKE2, provider, *s.clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(s.T(), err)
}

func (s *NodeScalingTestSuite) TestScalingNodePools() {
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
		name          string
		nodeRoles     machinepools.NodeRoles
		scaleQuantity int32
		cluster       *v1.SteveAPIObject
		isWindows     bool
	}{
		{"RKE2_Scale_Control_Plane", nodeRolesControlPlane, 1, s.cluster, false},
		{"RKE2_Scale_ETCD", nodeRolesEtcd, 1, s.cluster, false},
		{"RKE2_Scale_Worker", nodeRolesWorker, 1, s.cluster, false},
		{"RKE2_Scale_Windows", nodeRolesWindows, 1, s.cluster, true},
	}

	for _, tt := range tests {
		var err error
		s.Run(tt.name, func() {
			if s.clusterConfig.Provider != "vsphere" && tt.isWindows {
				s.T().Skip("Windows test requires access to vSphere")
			}

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

			tt.nodeRoles.Quantity = -1
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

func TestNodeScalingTestSuite(t *testing.T) {
	suite.Run(t, new(NodeScalingTestSuite))
}

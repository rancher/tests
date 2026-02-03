//go:build (validation || infra.rke2k3s || recurring || cluster.nodedriver || extended) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !sanity && !stress

package rke2k3s

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
	"github.com/rancher/tests/actions/scalinginput"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type NodeReplacingExistingClusterTestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	cattleConfig map[string]any
	clusterObject  *v1.SteveAPIObject
}

func (s *NodeReplacingExistingClusterTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *NodeReplacingExistingClusterTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)

	s.client = client

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(s.T(), err)

	rke2ClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, rke2ClusterConfig)

	k3sClusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, k3sClusterConfig)

	
	s.clusterObject, err = client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
}

func (s *NodeReplacingExistingClusterTestSuite) TestReplacingNodes() {
	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd: true,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker: true,
	}

	tests := []struct {
		name      string
		nodeRoles machinepools.NodeRoles
		clusterID string
	}{
		{"RKE2_Node_Driver_Replace_Control_Plane", nodeRolesControlPlane, s.clusterObject.ID},
		{"RKE2_Node_Driver_Replace_ETCD", nodeRolesEtcd, s.clusterObject.ID},
		{"RKE2_Node_Driver_Replace_Worker", nodeRolesWorker, s.clusterObject.ID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			err := scalinginput.ReplaceNodes(s.client, cluster.Name, tt.nodeRoles.Etcd, tt.nodeRoles.ControlPlane, tt.nodeRoles.Worker)
			require.NoError(s.T(), err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(s.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			require.NoError(s.T(), pods.VerifyClusterPods(s.client, cluster))
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestNodeReplacingExistingClusterTestSuite(t *testing.T) {
	suite.Run(t, new(NodeReplacingExistingClusterTestSuite))
}

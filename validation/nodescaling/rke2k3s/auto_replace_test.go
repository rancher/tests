//go:build (validation || extended) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !cluster.any && !cluster.custom && !cluster.nodedriver && !sanity && !stress

package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/scalinginput"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AutoReplaceSuite struct {
	suite.Suite
	client        *rancher.Client
	session       *session.Session
	cattleConfig  map[string]any
	rke2ClusterID string
	k3sClusterID  string
}

func (s *AutoReplaceSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *AutoReplaceSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)

	s.client = client

	standardUserClient, err := standard.CreateStandardUser(s.client)
	require.NoError(s.T(), err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig.MachinePools = nodeRolesStandard

	s.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), clusterConfig, true, false)
	require.NoError(s.T(), err)

	s.k3sClusterID, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.K3SClusterType.String(), clusterConfig, true, false)
	require.NoError(s.T(), err)
}

func (s *AutoReplaceSuite) TestAutoReplace() {
	tests := []struct {
		name      string
		role      string
		clusterID string
	}{
		{"Auto_replace_RKE2_ETCD", "etcd", s.rke2ClusterID},
		{"Auto_replace_RKE2_controlplane", "control-plane", s.rke2ClusterID},
		{"Auto_replace_RKE2_worker", "worker", s.rke2ClusterID},
		{"Auto_replace_K3S_ETCD", "etcd", s.k3sClusterID},
		{"Auto_replace_K3S_controlplane", "control-plane", s.k3sClusterID},
		{"Auto_replace_K3S_worker", "worker", s.k3sClusterID},
	}

	for _, tt := range tests {
		s.Run(tt.name, func() {
			err := scalinginput.AutoReplaceFirstNodeWithRole(s.client, s.client.RancherConfig.ClusterName, tt.role)
			require.NoError(s.T(), err)
		})
	}
}

func TestAutoReplaceSuite(t *testing.T) {
	suite.Run(t, new(AutoReplaceSuite))
}

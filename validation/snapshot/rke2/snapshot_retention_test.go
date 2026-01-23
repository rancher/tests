//go:build (validation || extended || infra.any || cluster.any) && !sanity && !stress

package rke2

import (
	"fmt"
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type SnapshotRetentionTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	cattleConfig   map[string]any
	cluster        *v1.SteveAPIObject
	snapshotConfig *SnapshotRetentionConfig
}

type SnapshotRetentionConfig struct {
	ClusterName       string `json:"clusterName" yaml:"clusterName"`
	SnapshotInterval  int    `json:"snapshotInterval" yaml:"snapshotInterval"`
	SnapshotRetention int    `json:"snapshotRetention" yaml:"snapshotRetention"`
}

func (s *SnapshotRetentionTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotRetentionTestSuite) SetupSuite() {
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

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

		logrus.Info("Provisioning RKE2 cluster")
		s.cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, false, false)
		require.NoError(s.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		s.cluster, err = client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.client.RancherConfig.ClusterName)
		require.NoError(s.T(), err)
	}
}

func (s *SnapshotRetentionTestSuite) TestAutomaticSnapshotRetention() {
	tests := []struct {
		testName                 string
		cluster                  *v1.SteveAPIObject
		retentionLimit           int
		intervalBetweenSnapshots int
	}{
		{"RKE2_Retention_Limit", s.cluster, 2, 1},
	}

	for _, tt := range tests {
		s.Run(tt.testName, func() {
			cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.cluster.ID)
			require.NoError(s.T(), err)

			clusterObject, clusterResponse, err := extClusters.GetProvisioningClusterByName(s.client, cluster.Name, namespaces.FleetDefault)
			require.NoError(s.T(), err)

			clusterObject.Spec.RKEConfig.ETCD.SnapshotRetention = tt.retentionLimit
			cronSchedule := fmt.Sprintf("%s%v%s", "*/", tt.intervalBetweenSnapshots, " * * * *")
			clusterObject.Spec.RKEConfig.ETCD.SnapshotScheduleCron = cronSchedule

			_, err = s.client.Steve.SteveType(stevetypes.Provisioning).Update(clusterResponse, clusterObject)
			require.NoError(s.T(), err)

			err = etcdsnapshot.CreateSnapshotsUntilRetentionLimit(s.client, cluster.Name, tt.retentionLimit, tt.intervalBetweenSnapshots)
			require.NoError(s.T(), err)
		})
	}
}

func TestSnapshotRetentionTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotRetentionTestSuite))
}

//go:build validation || recurring

package ipv6

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	containerImage = "nginx"
)

type SnapshotIPv6RestoreTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	rke2Cluster  *v1.SteveAPIObject
}

func (s *SnapshotIPv6RestoreTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SnapshotIPv6RestoreTestSuite) SetupSuite() {
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

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

	logrus.Info("Provisioning RKE2 cluster")
	s.rke2Cluster, err = resources.ProvisionRKE2K3SCluster(s.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, false, false)
	require.NoError(s.T(), err)
}

func snapshotRestoreConfigs() []*etcdsnapshot.Config {
	return []*etcdsnapshot.Config{
		{
			UpgradeKubernetesVersion: "",
			SnapshotRestore:          "none",
			RecurringRestores:        1,
		},
		{
			UpgradeKubernetesVersion: "",
			SnapshotRestore:          "kubernetesVersion",
			RecurringRestores:        1,
		},
		{
			UpgradeKubernetesVersion:     "",
			SnapshotRestore:              "all",
			ControlPlaneConcurrencyValue: "15%",
			WorkerConcurrencyValue:       "20%",
			RecurringRestores:            1,
		},
	}
}

func (s *SnapshotIPv6RestoreTestSuite) TestSnapshotIPv6Restore() {
	snapshotRestoreConfigRKE2 := snapshotRestoreConfigs()

	tests := []struct {
		name         string
		etcdSnapshot *etcdsnapshot.Config
		clusterID    string
	}{
		{"RKE2_IPv6_Restore_ETCD", snapshotRestoreConfigRKE2[0], s.rke2Cluster.ID},
		{"RKE2_IPv6_Restore_ETCD_K8sVersion", snapshotRestoreConfigRKE2[1], s.rke2Cluster.ID},
		{"RKE2_IPv6_Restore_Upgrade_Strategy", snapshotRestoreConfigRKE2[2], s.rke2Cluster.ID},
	}

	for _, tt := range tests {
		cluster, err := s.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(s.T(), err)

		s.Run(tt.name, func() {
			err := etcdsnapshot.CreateAndValidateSnapshotRestore(s.client, cluster.Name, tt.etcdSnapshot, containerImage)
			require.NoError(s.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestSnapshotIPv6RestoreTestSuite(t *testing.T) {
	suite.Run(t, new(SnapshotIPv6RestoreTestSuite))
}

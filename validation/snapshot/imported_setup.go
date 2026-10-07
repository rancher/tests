package snapshot

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/etcdsnapshot"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	tfpImported "github.com/rancher/tfp-automation/tests/infrastructure/downstream/imported"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type importedSnapshotTest struct {
	suite.Suite
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	Session            *session.Session
	CattleConfig       map[string]any
	ClusterConfig      *clusters.ClusterConfig
	rancherConfig      *rancher.Config
	SnapshotConfig     *etcdsnapshot.Config
	Cluster            *v1.SteveAPIObject
	NestedModuleDir    string
	TerraformOptions   *terraform.Options
}

func ImportedSetup(t *testing.T, clusterType string) *importedSnapshotTest {
	s := &importedSnapshotTest{}

	testSession := session.NewSession()
	s.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	s.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.Client)
	require.NoError(t, err)

	s.StandardUserClient = standardUserClient

	s.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.CattleConfig, err = defaults.LoadPackageDefaults(s.CattleConfig, "")
	require.NoError(t, err)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.CattleConfig, rancherConfig)

	s.rancherConfig = rancherConfig

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	snapshotConfig := new(etcdsnapshot.Config)
	operations.LoadObjectFromMap(etcdsnapshot.ConfigurationFileKey, s.CattleConfig, snapshotConfig)

	s.SnapshotConfig = snapshotConfig

	if rancherConfig.ClusterName == "" {
		nodeRolesStandard := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

		rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(s.CattleConfig)
		terratestConfig.Nodepools = nodeRolesStandard

		logrus.Info("Provisioning imported cluster")
		s.NestedModuleDir, s.TerraformOptions, _, s.Cluster = tfpImported.CreateImportedCluster(t, s.StandardUserClient, rancherConfig, terraformConfig, terratestConfig, clusterType, "validation/provisioning/"+clusterType)

		logrus.Infof("Verifying the imported cluster is ready (%s)", s.Cluster.Name)
		require.NoError(t, provisioning.VerifyClusterReadyV3(s.Client, s.Cluster.Name))

		logrus.Infof("Verifying cluster deployments (%s)", s.Cluster.Name)
		err = deployment.VerifyClusterDeployments(s.Client, s.Cluster)
		require.NoError(t, err)

		logrus.Infof("Verifying cluster pods (%s)", s.Cluster.Name)
		err = pods.VerifyClusterPods(s.Client, s.Cluster)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster (%s)", rancherConfig.ClusterName)
		s.Cluster, err = clusters.GetClusterByName(client, s.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return s
}

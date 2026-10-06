package encryptionkeyrotation

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

type importedEncryptionKeyRotationTest struct {
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

func ImportedSetup(t *testing.T, clusterType string) *importedEncryptionKeyRotationTest {
	e := &importedEncryptionKeyRotationTest{}

	testSession := session.NewSession()
	e.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	e.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(e.Client)
	require.NoError(t, err)

	e.StandardUserClient = standardUserClient

	e.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	e.CattleConfig, err = defaults.LoadPackageDefaults(e.CattleConfig, "")
	require.NoError(t, err)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, e.CattleConfig, rancherConfig)

	e.rancherConfig = rancherConfig

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, e.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	snapshotConfig := new(etcdsnapshot.Config)
	operations.LoadObjectFromMap(etcdsnapshot.ConfigurationFileKey, e.CattleConfig, snapshotConfig)

	e.SnapshotConfig = snapshotConfig

	if rancherConfig.ClusterName == "" {
		nodeRolesStandard := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

		rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(e.CattleConfig)
		terratestConfig.Nodepools = nodeRolesStandard

		logrus.Info("Provisioning imported cluster")
		e.NestedModuleDir, e.TerraformOptions, _, e.Cluster = tfpImported.CreateImportedCluster(t, e.StandardUserClient, rancherConfig, terraformConfig, terratestConfig, clusterType, "validation/provisioning/"+clusterType)

		logrus.Infof("Verifying the imported cluster is ready (%s)", e.Cluster.Name)
		require.NoError(t, provisioning.VerifyClusterReadyV3(e.Client, e.Cluster.Name))

		logrus.Infof("Verifying cluster deployments (%s)", e.Cluster.Name)
		err = deployment.VerifyClusterDeployments(e.Client, e.Cluster)
		require.NoError(t, err)

		logrus.Infof("Verifying cluster pods (%s)", e.Cluster.Name)
		err = pods.VerifyClusterPods(e.Client, e.Cluster)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster (%s)", rancherConfig.ClusterName)
		e.Cluster, err = clusters.GetClusterByName(client, e.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return e
}

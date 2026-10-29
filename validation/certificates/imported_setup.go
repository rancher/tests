package certificates

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

type importedCertificatesTest struct {
	suite.Suite
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	Session            *session.Session
	CattleConfig       map[string]any
	ClusterConfig      *clusters.ClusterConfig
	rancherConfig      *rancher.Config
	Cluster            *v1.SteveAPIObject
	NestedModuleDir    string
	TerraformOptions   *terraform.Options
}

func ImportedSetup(t *testing.T, clusterType string) *importedCertificatesTest {
	c := &importedCertificatesTest{}

	testSession := session.NewSession()
	c.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	c.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(c.Client)
	require.NoError(t, err)

	c.StandardUserClient = standardUserClient

	c.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	c.CattleConfig, err = defaults.LoadPackageDefaults(c.CattleConfig, "")
	require.NoError(t, err)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, c.CattleConfig, rancherConfig)

	c.rancherConfig = rancherConfig

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	if rancherConfig.ClusterName == "" {
		nodeRolesStandard := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

		rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(c.CattleConfig)
		terratestConfig.Nodepools = nodeRolesStandard

		logrus.Info("Provisioning imported cluster")
		c.NestedModuleDir, c.TerraformOptions, _, c.Cluster = tfpImported.CreateImportedCluster(t, c.StandardUserClient, rancherConfig, terraformConfig, terratestConfig, clusterType, "validation/provisioning/"+clusterType)

		logrus.Infof("Verifying the imported cluster is ready (%s)", c.Cluster.Name)
		require.NoError(t, provisioning.VerifyClusterReadyV3(c.Client, c.Cluster.Name))

		logrus.Infof("Verifying cluster deployments (%s)", c.Cluster.Name)
		err = deployment.VerifyClusterDeployments(c.Client, c.Cluster)
		require.NoError(t, err)

		logrus.Infof("Verifying cluster pods (%s)", c.Cluster.Name)
		err = pods.VerifyClusterPods(c.Client, c.Cluster)
		require.NoError(t, err)
	} else {
		logrus.Infof("Using existing cluster (%s)", rancherConfig.ClusterName)
		c.Cluster, err = clusters.GetClusterByName(client, c.rancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return c
}

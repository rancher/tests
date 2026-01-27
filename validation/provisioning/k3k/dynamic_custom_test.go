//go:build validation || pit.daily

package k3k

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type dynamicCustomTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func dynamicCustomSetup(t *testing.T) dynamicCustomTest {
	var k dynamicCustomTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(k.client, defaults.K3K, k.cattleConfig)
	require.NoError(t, err)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	require.NoError(t, err)

	return k
}

func TestDynamicCustom(t *testing.T) {
	k := dynamicCustomSetup(t)
	t.Cleanup(func() {
		logrus.Info("Running cleanup")
		k.session.Cleanup()
	})

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

	externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, k.cattleConfig, awsEC2Configs)

	logrus.Info("Provisioning cluster")
	cluster, err := provisioning.CreateProvisioningCustomCluster(k.standardUserClient, &externalNodeProvider, clusterConfig, awsEC2Configs)
	require.NoError(t, err)

	logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
	provisioning.VerifyClusterReady(t, k.standardUserClient, cluster)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(k.standardUserClient, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(k.standardUserClient, cluster)
	require.NoError(t, err)

	params := provisioning.GetCustomSchemaParams(k.standardUserClient, k.cattleConfig)
	err = qase.UpdateSchemaParameters("K3K_Dynamic_Custom", params)
	if err != nil {
		logrus.Warningf("Failed to upload schema parameters %s", err)
	}
}

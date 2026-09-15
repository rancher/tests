//go:build os

package os

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type rebootTest struct {
	client       *rancher.Client
	session      *session.Session
	cattleConfig map[string]any
}

func rebootSetup(t *testing.T) rebootTest {
	var r rebootTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	r.cattleConfig, err = defaults.LoadSecretsManagerDefaults(r.cattleConfig)
	require.NoError(t, err)

	err = defaults.VerifyCattleConfig(r.cattleConfig, nil)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	return r
}

func TestReboot(t *testing.T) {
	t.Parallel()
	r := rebootSetup(t)

	clusterName := r.client.RancherConfig.ClusterName
	require.NotEmpty(t, clusterName, "rancher.clusterName must reference an existing cluster")

	t.Cleanup(func() {
		logrus.Infof("Running cleanup (%s)", clusterName)
		r.session.Cleanup()
	})

	logrus.Infof("Using existing cluster (%s)", clusterName)
	cluster, err := clusters.GetClusterByName(r.client, clusterName)
	require.NoError(t, err)

	logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
	err = provisioning.VerifyClusterReady(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
	err = clusters.VerifyServiceAccountTokenSecret(r.client, cluster.Name)
	require.NoError(t, err)

	logrus.Infof("Rebooting one node of each role (%s)", cluster.Name)
	err = rebootNodeRoles(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
	err = provisioning.VerifyClusterReady(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
	err = deployment.VerifyClusterDeployments(r.client, cluster)
	require.NoError(t, err)

	logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
	err = pods.VerifyClusterPods(r.client, cluster)
	require.NoError(t, err)
}

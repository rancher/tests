//go:build os

package os

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
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
	t.Cleanup(r.session.Cleanup)

	tests := []struct {
		name    string
		k8sType string
	}{
		{"OS_RKE2_Reboot", defaults.RKE2},
		{"OS_K3S_Reboot", defaults.K3S},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			testSession := session.NewSession()
			t.Cleanup(func() {
				logrus.Infof("Running fallback cleanup (%s)", tt.name)
				testSession.Cleanup()
			})

			testClient, err := r.client.WithSession(testSession)
			require.NoError(t, err)

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

			versions, err := kubernetesversions.Default(testClient, tt.k8sType, nil)
			require.NoError(t, err)
			clusterConfig.KubernetesVersion = versions[0]

			require.NotNil(t, clusterConfig.Provider)
			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

			logrus.Infof("Provisioning cluster for reboot test (%s)", tt.name)
			cluster, err := provisioning.CreateProvisioningCluster(testClient, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(testClient, cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Rebooting one node of each role (%s)", cluster.Name)
			err = rebootNodeRoles(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(testClient, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(r.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

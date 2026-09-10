//go:build os

package os

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/framework/cleanup"
	tfpImported "github.com/rancher/tfp-automation/tests/infrastructure/downstream/imported"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type importedTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func importedSetup(t *testing.T) importedTest {
	var r importedTest
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

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestImported(t *testing.T) {
	t.Parallel()
	r := importedSetup(t)

	tests := []struct {
		name    string
		k8sType string
		client  *rancher.Client
	}{
		{"OS_RKE2_Imported", defaults.RKE2, r.standardUserClient},
		{"OS_K3S_Imported", defaults.K3S, r.standardUserClient},
	}
	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var err error

			rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(r.cattleConfig)
			versions, err := kubernetesversions.Default(r.client, tt.k8sType, nil)
			require.NoError(t, err)
			terratestConfig.KubernetesVersion = versions[0]

			logrus.Info("Provisioning imported cluster")
			nestedRancherModuleDir, perTestTerraformOptions, _, cluster := tfpImported.CreateImportedCluster(t, tt.client, rancherConfig, terraformConfig, terratestConfig, tt.k8sType, "validation/provisioning/"+tt.k8sType)
			defer os.RemoveAll(nestedRancherModuleDir)
			defer cleanup.Cleanup(t, perTestTerraformOptions, nestedRancherModuleDir)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReadyV3(r.client, cluster.Name)
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

			workloadConfigs := new(workloads.Workloads)
			operations.LoadObjectFromMap(workloads.WorkloadsConfigurationFileKey, r.cattleConfig, workloadConfigs)

			logrus.Infof("Creating workloads (%s)", cluster.Name)
			createdWorkloads, err := workloads.CreateWorkloads(tt.client, cluster.Name, *workloadConfigs)
			require.NoError(t, err)

			logrus.Infof("Verifying workloads (%s)", cluster.Name)
			_, err = workloads.VerifyWorkloads(tt.client, cluster.Name, *createdWorkloads)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(r.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

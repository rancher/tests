//go:build validation || (recurring && dualstack) || dualstack

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/framework/cleanup"
	tfpImported "github.com/rancher/tfp-automation/tests/infrastructure/downstream/imported"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type importK3SDualstackTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func importK3SDualstackSetup(t *testing.T) importK3SDualstackTest {
	var k importK3SDualstackTest

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

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	require.NoError(t, err)

	return k
}

func TestImportK3SDualstack(t *testing.T) {
	t.Parallel()
	k := importK3SDualstackSetup(t)

	nodeRolesAll := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true, Controlplane: true, Worker: true}}
	nodeRolesShared := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true, Controlplane: true}, {Quantity: 1, Worker: true}}
	nodeRolesDedicated := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true}, {Quantity: 1, Controlplane: true}, {Quantity: 1, Worker: true}}
	nodeRolesStandard := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

	tests := []struct {
		name      string
		client    *rancher.Client
		nodePools []tfpConfig.Nodepool
	}{
		{"K3S_Dualstack_Imported|etcd_cp_worker", k.standardUserClient, nodeRolesAll},
		{"K3S_Dualstack_Imported|etcd_cp|worker", k.standardUserClient, nodeRolesShared},
		{"K3S_Dualstack_Imported|etcd|cp|worker", k.standardUserClient, nodeRolesDedicated},
		{"K3S_Dualstack_Imported|3_etcd|2_cp|3_worker", k.standardUserClient, nodeRolesStandard},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var err error

			rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(k.cattleConfig)
			terratestConfig.Nodepools = tt.nodePools

			logrus.Info("Provisioning imported cluster")
			nestedRancherModuleDir, perTestTerraformOptions, _, cluster := tfpImported.CreateImportedCluster(t, tt.client, rancherConfig, terraformConfig, terratestConfig, defaults.K3S, "validation/provisioning/dualstack")
			defer os.RemoveAll(nestedRancherModuleDir)
			defer cleanup.Cleanup(t, perTestTerraformOptions, nestedRancherModuleDir)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReadyV3(k.client, cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(k.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(k.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, k.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

//go:build validation || recurring

package k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/reports"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type hardenedTest struct {
	client              *rancher.Client
	session             *session.Session
	standardUserClient  *rancher.Client
	cattleConfig        map[string]any
	project             *management.Project
	chartInstallOptions *charts.InstallOptions
}

func hardenedSetup(t *testing.T) hardenedTest {
	var k hardenedTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)

	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	assert.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	assert.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(client, defaults.K3S, k.cattleConfig)
	assert.NoError(t, err)

	k.standardUserClient, err = standard.CreateStandardUser(k.client)
	assert.NoError(t, err)

	return k
}

func TestHardened(t *testing.T) {
	t.Parallel()
	k := hardenedSetup(t)
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name            string
		client          *rancher.Client
		machinePools    []provisioninginput.MachinePools
		scanProfileName string
	}{
		{"K3S_CIS_1.9_Profile|3_etcd|2_cp|3_worker", k.standardUserClient, nodeRolesStandard, "k3s-cis-1.9-profile"},
	}
	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.machinePools
			clusterConfig.Hardened = true

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, k.cattleConfig, awsEC2Configs)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
			reports.TimeoutClusterReport(cluster, err)
			assert.NoError(t, err)

			logrus.Infof("Verifying cluster (%s)", cluster.Name)
			provisioning.VerifyCluster(t, tt.client, cluster)

			clusterMeta, err := extensionscluster.NewClusterMeta(tt.client, cluster.Name)
			reports.TimeoutClusterReport(cluster, err)
			assert.NoError(t, err)

			latestCISBenchmarkVersion, err := tt.client.Catalog.GetLatestChartVersion(charts.CISBenchmarkName, catalog.RancherChartRepo)
			assert.NoError(t, err)

			project, err := projects.GetProjectByName(tt.client, cluster.ID, cis.System)
			reports.TimeoutClusterReport(cluster, err)
			assert.NoError(t, err)

			k.project = project
			require.NotEmpty(t, k.project)

			k.chartInstallOptions = &charts.InstallOptions{
				Cluster:   clusterMeta,
				Version:   latestCISBenchmarkVersion,
				ProjectID: k.project.ID,
			}

			logrus.Infof("Running CIS Benchmark on cluster (%s)", cluster.Name)
			cis.SetupCISBenchmarkChart(tt.client, k.project.ClusterID, k.chartInstallOptions, charts.CISBenchmarkNamespace)
			cis.RunCISScan(tt.client, k.project.ClusterID, tt.scanProfileName)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, k.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

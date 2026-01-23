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
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/reports"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
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
	require.NoError(t, err)

	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(client, defaults.K3S, k.cattleConfig)
	require.NoError(t, err)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	require.NoError(t, err)

	return k
}

func TestHardened(t *testing.T) {
	t.Parallel()
	k := hardenedSetup(t)

	tests := []struct {
		name            string
		client          *rancher.Client
		scanProfileName string
	}{
		{"K3S_CIS_1.9_Profile|3_etcd|2_cp|3_worker", k.standardUserClient, "k3s-cis-1.9-profile"},
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
			clusterConfig.Hardened = true

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, k.cattleConfig, awsEC2Configs)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
			reports.TimeoutClusterReport(cluster, err)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, tt.client, cluster)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(tt.client, cluster)
			require.NoError(t, err)

			chartName := charts.CISBenchmarkName
			chartNamespace := charts.CISBenchmarkNamespace
			if clusterConfig.Compliance {
				chartName = charts.ComplianceName
				chartNamespace = charts.ComplianceNamespace
			}

			clusterMeta, err := extensionscluster.NewClusterMeta(tt.client, cluster.Name)
			reports.TimeoutClusterReport(cluster, err)
			require.NoError(t, err)

			latestCISBenchmarkVersion, err := tt.client.Catalog.GetLatestChartVersion(chartName, catalog.RancherChartRepo)
			require.NoError(t, err)

			project, err := projects.GetProjectByName(tt.client, clusterMeta.ID, cis.System)
			reports.TimeoutClusterReport(cluster, err)
			require.NoError(t, err)

			k.project = project
			require.NotEmpty(t, k.project)

			k.chartInstallOptions = &charts.InstallOptions{
				Cluster:   clusterMeta,
				Version:   latestCISBenchmarkVersion,
				ProjectID: k.project.ID,
			}

			logrus.Infof("Setting up %s on cluster (%s)", chartName, cluster.Name)
			cis.SetupHardenedChart(tt.client, k.project.ClusterID, k.chartInstallOptions, chartName, chartNamespace)

			logrus.Infof("Running CIS scan on cluster (%s)", cluster.Name)
			cis.RunCISScan(tt.client, k.project.ClusterID, tt.scanProfileName)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, k.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

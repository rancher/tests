//go:build validation || recurring

package rke2

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
	var r hardenedTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestHardened(t *testing.T) {
	t.Parallel()
	r := hardenedSetup(t)

	tests := []struct {
		name            string
		client          *rancher.Client
		scanProfileName string
	}{
		{"RKE2_CIS_1.9_Profile|3_etcd|2_cp|3_worker", r.client, "rke2-cis-1.9-profile"},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
			clusterConfig.Hardened = true

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)

			logrus.Infof("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
			reports.TimeoutClusterReport(cluster, err)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, r.client, cluster)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(r.client, cluster)
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

			latestHardenedChartVersion, err := tt.client.Catalog.GetLatestChartVersion(chartName, catalog.RancherChartRepo)
			require.NoError(t, err)

			project, err := projects.GetProjectByName(tt.client, clusterMeta.ID, cis.System)
			reports.TimeoutClusterReport(cluster, err)
			require.NoError(t, err)

			r.project = project
			require.Equal(t, r.project.Name, cis.System)

			r.chartInstallOptions = &charts.InstallOptions{
				Cluster:   clusterMeta,
				Version:   latestHardenedChartVersion,
				ProjectID: r.project.ID,
			}

			logrus.Infof("Setting up %s on cluster (%s)", chartName, cluster.Name)
			cis.SetupHardenedChart(tt.client, r.project.ClusterID, r.chartInstallOptions, chartName, chartNamespace)

			logrus.Infof("Running CIS scan on cluster (%s)", cluster.Name)
			cis.RunCISScan(tt.client, r.project.ClusterID, tt.scanProfileName)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

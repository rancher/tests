//go:build (validation || infra.any || cluster.any || sanity || pit.daily || pit.elemental.daily) && !stress && !extended

package charts

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LoggingTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	project *management.Project
	cluster *clusters.ClusterMeta
}

func (l *LoggingTestSuite) TearDownSuite() {
	l.session.Cleanup()
}

func (l *LoggingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	l.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(l.T(), err)
	l.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(l.T(), clusterName, "Cluster name to install is not set")

	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(l.T(), err)
	l.cluster = cluster

	projectConfig := &management.Project{
		ClusterID: cluster.ID,
		Name:      charts.SystemProject,
	}

	l.T().Logf("Creating project [%s] on cluster [%s]", charts.SystemProject, cluster.Name)
	createdProject, err := client.Management.Project.Create(projectConfig)
	require.NoError(l.T(), err)
	require.Equal(l.T(), charts.SystemProject, createdProject.Name)

	l.project = createdProject
}

func (l *LoggingTestSuite) TestLoggingInstallation() {
	l.T().Logf("Resolving latest chart version for [%s] from repository [%s]", charts.LonghornNamespace, charts.LonghornChartName)
	longhornChart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	if !longhornChart.IsAlreadyInstalled {
		// Get latest versions of longhorn
		latestLonghornVersion, err := l.client.Catalog.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
		require.NoError(l.T(), err)

		payloadOpts := charts.PayloadOpts{
			Namespace: charts.LonghornNamespace,
			Host:      l.client.RancherConfig.Host,
			InstallOptions: charts.InstallOptions{
				Cluster:   l.cluster,
				Version:   latestLonghornVersion,
				ProjectID: l.project.ID,
			},
		}

		l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, payloadOpts.Version, l.project.Name, payloadOpts.Namespace)
		err = charts.InstallLonghornChart(l.client, payloadOpts, nil)
		require.NoError(l.T(), err)
	}

	loggingChart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.RancherLoggingNamespace, charts.RancherLoggingName)
	require.NoError(l.T(), err)

	if !loggingChart.IsAlreadyInstalled {
		l.T().Logf("Resolving latest chart version for [%s] from repository [%s]", charts.RancherLoggingName, catalog.RancherChartRepo)
		latestLoggingVersion, err := l.client.Catalog.GetLatestChartVersion(charts.RancherLoggingName, catalog.RancherChartRepo)
		require.NoError(l.T(), err)

		installOptions := &charts.InstallOptions{
			Cluster:   l.cluster,
			Version:   latestLoggingVersion,
			ProjectID: l.project.ID,
		}

		featureOptions := &charts.RancherLoggingOpts{
			AdditionalLoggingSources: true,
			LoggingEnabledSources:    true,
		}

		l.T().Logf("Installing Rancher Logging chart on cluster [%s] with version [%s]", l.cluster.Name, latestLoggingVersion)
		err = charts.InstallRancherLoggingChart(l.client, installOptions, featureOptions)
		require.NoError(l.T(), err)
	}

	l.T().Logf("Waiting for logging deamonsets to become ready in namespace [%s]", charts.RancherLoggingNamespace)
	err = shepherdCharts.WatchAndWaitDaemonSets(l.client, l.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	require.NoError(l.T(), err, "FluentBit DaemonSets not ready")

	l.T().Logf("Waiting for logging deployments to become ready in namespace [%s]", charts.RancherLoggingNamespace)
	err = shepherdCharts.WatchAndWaitDeployments(l.client, l.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	require.NoError(l.T(), err, "rancher-logging Deployments not ready")

	l.T().Logf("Waiting for logging statefulsets to become ready in namespace [%s]", charts.RancherLoggingNamespace)
	err = shepherdCharts.WatchAndWaitStatefulSets(l.client, l.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	require.NoError(l.T(), err, "Fluentd StatefulSets not ready")

	logs, err := verifyLoggingReceiver(l.client, l.cluster.ID)
	require.NoError(l.T(), err, "Verify Logging Receiver error")
	require.NotEmpty(l.T(), logs, "Logs are empty")
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

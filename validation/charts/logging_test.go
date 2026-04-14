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
	client              *rancher.Client
	session             *session.Session
	project             *management.Project
	cluster             *clusters.ClusterMeta
	chartInstallOptions *charts.InstallOptions
	chartFeatureOptions *charts.RancherLoggingOpts
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
	l.T().Logf("Resolving latest chart version for [%s] from repository [%s]", charts.RancherLoggingName, catalog.RancherChartRepo)
	latestLoggingVersion, err := l.client.Catalog.GetLatestChartVersion(charts.RancherLoggingName, catalog.RancherChartRepo)
	require.NoError(l.T(), err)

	l.chartInstallOptions = &charts.InstallOptions{
		Cluster:   l.cluster,
		Version:   latestLoggingVersion,
		ProjectID: l.project.ID,
	}

	l.chartFeatureOptions = &charts.RancherLoggingOpts{
		AdditionalLoggingSources: true,
	}
}

func (l *LoggingTestSuite) TestLoggingInstallation() {
	subSession := l.session.NewSession()
	defer subSession.Cleanup()

	client, err := l.client.WithSession(subSession)
	require.NoError(l.T(), err)

	l.T().Logf("Checking if logging chart is already installed in namespace [%s]", charts.RancherLoggingNamespace)
	loggingChartStatus, err := shepherdCharts.GetChartStatus(client, l.cluster.ID, charts.RancherLoggingNamespace, charts.RancherLoggingName)
	require.NoError(l.T(), err)

	if !loggingChartStatus.IsAlreadyInstalled {
		l.T().Logf("Installing Rancher Logging chart on cluster [%s] with version [%s]", l.cluster.Name, l.chartInstallOptions.Version)
		err = charts.InstallRancherLoggingChart(client, l.chartInstallOptions, l.chartFeatureOptions)
		require.NoError(l.T(), err)

		l.T().Logf("Waiting for logging Deployments to become ready in namespace [%s]", charts.RancherLoggingNamespace)
		err = shepherdCharts.WatchAndWaitDeployments(client, l.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
		require.NoError(l.T(), err)
	}

	l.T().Log("Verifying logging collectors are running")
	err = verifyLoggingCollectorsRunning(client, l.cluster.ID)
	require.NoError(l.T(), err)

	l.T().Log("Creating logging pipeline to verify pipeline is working")
	outputName, flowName, err := createLoggingPipeline(client, l.cluster.ID)
	require.NoError(l.T(), err)

	l.T().Logf("Verifying logging pipeline is active [%s] [%s]", outputName, flowName)
	err = verifyLoggingPipelineActive(client, l.cluster.ID, outputName, flowName)
	require.NoError(l.T(), err)
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

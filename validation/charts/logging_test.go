//go:build (validation || infra.any || cluster.any || sanity || pit.daily) && !stress && !extended

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
	}

	l.T().Logf("Installing Rancher Logging chart on cluster [%s] with version [%s]", l.cluster.Name, latestLoggingVersion)
	err = charts.InstallRancherLoggingChart(l.client, installOptions, featureOptions)
	require.NoError(l.T(), err)

	l.T().Logf("Waiting for logging deployments to become ready in namespace [%s]", charts.RancherLoggingNamespace)
	err = shepherdCharts.WatchAndWaitDeployments(l.client, l.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	require.NoError(l.T(), err)
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

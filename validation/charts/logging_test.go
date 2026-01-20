//go:build (validation || infra.rke1 || cluster.any || stress || pit.daily) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !sanity && !extended

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

func (i *LoggingTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *LoggingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	i.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(i.T(), err)
	i.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(i.T(), clusterName, "Cluster name to install is not set")

	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(i.T(), err)
	i.cluster = cluster

	projectConfig := &management.Project{
		ClusterID: cluster.ID,
		Name:      charts.SystemProject,
	}

	i.T().Logf("Creating project [%s] on cluster [%s]", charts.SystemProject, cluster.Name)
	createdProject, err := client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), charts.SystemProject, createdProject.Name)

	i.project = createdProject
}

func (i *LoggingTestSuite) TestLoggingInstallation() {
	i.T().Logf("Resolving latest chart version for [%s] from repository [%s]", charts.RancherLoggingName, catalog.RancherChartRepo)
	latestLoggingVersion, err := i.client.Catalog.GetLatestChartVersion(charts.RancherLoggingName, catalog.RancherChartRepo)
	require.NoError(i.T(), err)

	installOptions := &charts.InstallOptions{
		Cluster:   i.cluster,
		Version:   latestLoggingVersion,
		ProjectID: i.project.ID,
	}

	featureOptions := &charts.RancherLoggingOpts{
		AdditionalLoggingSources: true,
	}

	i.T().Logf("Installing Rancher Logging chart on cluster [%s] with version [%s]", i.cluster.Name, latestLoggingVersion)
	err = charts.InstallRancherLoggingChart(i.client, installOptions, featureOptions)
	require.NoError(i.T(), err)

	i.T().Logf("Waiting for logging deployments to become ready in namespace [%s]", charts.RancherLoggingNamespace)
	err = shepherdCharts.WatchAndWaitDeployments(i.client, i.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	require.NoError(i.T(), err)
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

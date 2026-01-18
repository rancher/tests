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
	client          *rancher.Client
	session         *session.Session
	project         *management.Project
	cluster         *clusters.ClusterMeta
	registrySetting *management.Setting
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

	// Get clusterName from config yaml
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(i.T(), clusterName, "Cluster name to install is not set")

	// Get cluster meta
	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(i.T(), err)

	i.cluster = cluster

	// Get Server and Registry Setting Values
	i.registrySetting, err = client.Management.Setting.ByID("system-default-registry")
	require.NoError(i.T(), err)

	// Create project
	projectConfig := &management.Project{
		ClusterID: cluster.ID,
		Name:      charts.SystemProject,
	}
	createdProject, err := client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), createdProject.Name, charts.SystemProject)
	i.project = createdProject
}

func (i *LoggingTestSuite) TestLoggingInstallation() {
	client, err := i.client.WithSession(i.session)
	require.NoError(i.T(), err)

	latestLoggingVersion, err := client.Catalog.GetLatestChartVersion(charts.RancherLoggingName, catalog.RancherChartRepo)
	require.NoError(i.T(), err)

	loggingChartInstallOption := &charts.InstallOptions{
		Cluster:   i.cluster,
		Version:   latestLoggingVersion,
		ProjectID: i.project.ID,
	}

	loggingChartFeatureOption := &charts.RancherLoggingOpts{
		AdditionalLoggingSources: true,
	}

	i.T().Logf("Installing logging chart with the latest version in cluster [%v] with version [%v]", i.cluster.Name, latestLoggingVersion)
	err = charts.InstallRancherLoggingChart(client, loggingChartInstallOption, loggingChartFeatureOption)
	require.NoError(i.T(), err)

	err = shepherdCharts.WatchAndWaitDeployments(client, i.cluster.ID, charts.RancherLoggingNamespace, metav1.ListOptions{})
	require.NoError(i.T(), err)
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

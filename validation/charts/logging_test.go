//go:build (validation || infra.rke1 || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !sanity && !extended

package charts

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/registries"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
		Name:      exampleAppProjectName,
	}
	createdProject, err := client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), createdProject.Name, exampleAppProjectName)
	i.project = createdProject
}

func (i *LoggingTestSuite) TestChartInstallation() {
	client, err := i.client.WithSession(i.session)
	require.NoError(i.T(), err)

	loggingChart, err := extencharts.GetChartStatus(client, i.project.ClusterID, charts.RancherLoggingNamespace, charts.RancherLoggingName)
	require.NoError(i.T(), err)

	if !loggingChart.IsAlreadyInstalled {
		// Get latest versions of logging
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
	}

	isUsingRegistry, err := registries.CheckAllClusterPodsForRegistryPrefix(client, i.cluster.ID, i.registrySetting.Value)
	require.NoError(i.T(), err)
	assert.Truef(i.T(), isUsingRegistry, "Checking if using correct registry prefix")
}

func TestLoggingTestSuite(t *testing.T) {
	suite.Run(t, new(LoggingTestSuite))
}

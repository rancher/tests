//go:build (validation || infra.any || i.cluster.any || sanity || pit.daily) && !stress && !extended

package charts

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/projects"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CisBenchmarkTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
	project *management.Project
}

func (i *CisBenchmarkTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *CisBenchmarkTestSuite) SetupSuite() {
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

	clusterMeta, err := extensionscluster.NewClusterMeta(i.client, i.cluster.Name)
	require.NoError(i.T(), err)

	i.project, err = projects.GetProjectByName(i.client, clusterMeta.ID, cis.System)
	require.NoError(i.T(), err)
	require.Equal(i.T(), i.project.Name, cis.System)
}

func (i *CisBenchmarkTestSuite) TestCISBenchmarkInstallation() {
	chartName := charts.CISBenchmarkName
	chartNamespace := charts.CISBenchmarkNamespace

	clusterMeta, err := extensionscluster.NewClusterMeta(i.client, i.cluster.Name)
	require.NoError(i.T(), err)

	latestHardenedChartVersion, err := i.client.Catalog.GetLatestChartVersion(chartName, catalog.RancherChartRepo)
	require.NoError(i.T(), err)

	chartInstallOptions := &charts.InstallOptions{
		Cluster:   clusterMeta,
		Version:   latestHardenedChartVersion,
		ProjectID: i.project.ID,
	}

	i.T().Logf("Setting up %s on cluster (%s)", chartName, i.cluster.Name)
	err = cis.SetupHardenedChart(i.client, i.project.ClusterID, chartInstallOptions, chartName, chartNamespace)
	require.NoError(i.T(), err)

	i.T().Logf("Running CIS scan on cluster (%s)", i.cluster.Name)
	err = cis.RunCISScan(i.client, i.project.ClusterID, cis.ScanProfileName)
	require.NoError(i.T(), err)
}

func TestCisBenchmarkTestSuite(t *testing.T) {
	suite.Run(t, new(CisBenchmarkTestSuite))
}

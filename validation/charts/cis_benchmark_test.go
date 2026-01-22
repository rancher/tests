//go:build (validation || infra.any || cluster.any || sanity || pit.daily) && !stress && !extended

package charts

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
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

func (c *CisBenchmarkTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CisBenchmarkTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)
	c.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(c.T(), clusterName, "Cluster name to install is not set")

	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(c.T(), err)
	c.cluster = cluster

	c.T().Logf("Creating Project [%s]", cis.System)
	c.project, err = projects.GetProjectByName(c.client, cluster.ID, cis.System)
	require.NoError(c.T(), err)
	require.Equal(c.T(), cis.System, c.project.Name)
}

func (c *CisBenchmarkTestSuite) TestCISBenchmarkInstallation() {
	chartName := charts.CISBenchmarkName
	c.T().Logf("Getting the latest chart version for [%s]", chartName)
	latestChartVersion, err := c.client.Catalog.GetLatestChartVersion(chartName, catalog.RancherChartRepo)
	require.NoError(c.T(), err)

	installOptions := &charts.InstallOptions{
		Cluster:   c.cluster,
		Version:   latestChartVersion,
		ProjectID: c.project.ID,
	}

	c.T().Logf("Installing %s chart on cluster [%s] with version [%s]", chartName, c.cluster.Name, latestChartVersion)
	err = cis.SetupHardenedChart(
		c.client,
		c.project.ClusterID,
		installOptions,
		chartName,
		charts.CISBenchmarkNamespace,
	)
	require.NoError(c.T(), err)

	c.T().Logf("Running CIS benchmark scan on cluster [%s] using profile [%s]", c.cluster.Name, cis.ScanProfileName)
	err = cis.RunCISScan(c.client, c.project.ClusterID, cis.ScanProfileName)
	require.NoError(c.T(), err)
}

func TestCisBenchmarkTestSuite(t *testing.T) {
	suite.Run(t, new(CisBenchmarkTestSuite))
}

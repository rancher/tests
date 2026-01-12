//go:build (validation || infra.any || i.cluster.any || sanity || pit.daily) && !stress && !extended

package charts

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/projects"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	"github.com/sirupsen/logrus"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CisBenchmarkTestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	cluster      *management.Cluster
	cattleConfig map[string]any
	project      *management.Project
}

func (i *CisBenchmarkTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *CisBenchmarkTestSuite) SetupSuite() {
	i.session = session.NewSession()

	client, err := rancher.NewClient("", i.session)
	require.NoError(i.T(), err)

	i.client = client

	i.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, i.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(i.T(), err)

	log.Info("Getting cluster name from the config file and append cluster details in connection")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(i.T(), clusterName, "Cluster name to install should be set")

	clusterID, err := clusters.GetClusterIDByName(i.client, clusterName)
	require.NoError(i.T(), err, "Error getting cluster ID")

	i.cluster, err = i.client.Management.Cluster.ByID(clusterID)
	require.NoError(i.T(), err)

	clusterMeta, err := extensionscluster.NewClusterMeta(i.client, i.cluster.Name)
	require.NoError(i.T(), err)

	i.project, err = projects.GetProjectByName(i.client, clusterMeta.ID, cis.System)
	require.NoError(i.T(), err)
}

func (i *CisBenchmarkTestSuite) TestInstallCisBenchmarkChart() {
	chartName := charts.CISBenchmarkName
	chartNamespace := charts.CISBenchmarkNamespace

	clusterMeta, err := extensionscluster.NewClusterMeta(i.client, i.cluster.Name)
	require.NoError(i.T(), err)

	latestHardenedChartVersion, err := i.client.Catalog.GetLatestChartVersion(chartName, catalog.RancherChartRepo)
	require.NoError(i.T(), err)

	i.project, err = projects.GetProjectByName(i.client, clusterMeta.ID, cis.System)
	require.NoError(i.T(), err)

	require.Equal(i.T(), i.project.Name, cis.System)

	chartInstallOptions := &charts.InstallOptions{
		Cluster:   clusterMeta,
		Version:   latestHardenedChartVersion,
		ProjectID: i.project.ID,
	}

	logrus.Infof("Setting up %s on cluster (%s)", chartName, i.cluster.Name)
	err = cis.SetupHardenedChart(i.client, i.project.ClusterID, chartInstallOptions, chartName, chartNamespace)
	require.NoError(i.T(), err)
}

func TestCisBenchmarkTestSuite(t *testing.T) {
	suite.Run(t, new(CisBenchmarkTestSuite))
}

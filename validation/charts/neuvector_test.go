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
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	NeuVectorProject = "System"
)

type NeuVectorTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
	project *management.Project
}

func (i *NeuVectorTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *NeuVectorTestSuite) SetupSuite() {
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

	i.project, err = projects.GetProjectByName(i.client, clusterMeta.ID, NeuVectorProject)
	require.NoError(i.T(), err)
}

func (i *NeuVectorTestSuite) TestInstallNeuVectorChart() {
	clusterMeta, err := extensionscluster.NewClusterMeta(i.client, i.cluster.Name)
	require.NoError(i.T(), err)

	latestNeuVectorChartVersion, err := i.client.Catalog.GetLatestChartVersion(charts.NeuVectorChartName, catalog.RancherChartRepo)
	require.NoError(i.T(), err)

	i.project, err = projects.GetProjectByName(i.client, clusterMeta.ID, NeuVectorProject)
	require.NoError(i.T(), err)
	require.Equal(i.T(), i.project.Name, NeuVectorProject)

	payloadOpts := charts.PayloadOpts{
		Namespace: charts.NeuVectorNamespace,
		InstallOptions: charts.InstallOptions{
			Cluster:   clusterMeta,
			Version:   latestNeuVectorChartVersion,
			ProjectID: i.project.ID,
		},
	}

	i.T().Logf("Setting up %s on cluster (%s)", charts.NeuVectorChartName, i.cluster.Name)
	err = charts.InstallNeuVectorChart(i.client, payloadOpts)
	require.NoError(i.T(), err)
}

func TestNeuVectorTestSuite(t *testing.T) {
	suite.Run(t, new(NeuVectorTestSuite))
}

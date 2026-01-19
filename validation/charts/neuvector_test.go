//go:build (validation || infra.any || i.cluster.any || sanity || pit.daily) && !stress && !extended

package charts

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	actionsCharts "github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/projects"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NeuVectorTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
	project *management.Project
}

func (n *NeuVectorTestSuite) TearDownSuite() {
	n.session.Cleanup()
}

func (n *NeuVectorTestSuite) SetupSuite() {
	testSession := session.NewSession()
	n.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(n.T(), err)

	n.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(n.T(), clusterName, "Cluster name to install is not set")

	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(n.T(), err)

	n.cluster = cluster

	n.T().Logf("Creating Project [%s]", actionsCharts.SystemProject)
	n.project, err = projects.GetProjectByName(n.client, cluster.ID, actionsCharts.SystemProject)
	require.NoError(n.T(), err)
	require.Equal(n.T(), actionsCharts.SystemProject, n.project.Name)
}

func (n *NeuVectorTestSuite) TestNeuVectorInstallation() {
	n.T().Logf("Getting the latest chart version for [%s]", actionsCharts.NeuVectorChartName)
	latestVersion, err := n.client.Catalog.GetLatestChartVersion(actionsCharts.NeuVectorChartName, catalog.RancherChartRepo)
	require.NoError(n.T(), err)

	payload := actionsCharts.PayloadOpts{
		Namespace: actionsCharts.NeuVectorNamespace,
		InstallOptions: actionsCharts.InstallOptions{
			Cluster:   n.cluster,
			Version:   latestVersion,
			ProjectID: n.project.ID,
		},
	}

	n.T().Logf("Installing NeuVector on cluster [%s]", n.cluster.Name)
	err = actionsCharts.InstallNeuVectorChart(n.client, payload)
	require.NoError(n.T(), err)

	n.T().Log("Waiting for NeuVector chart to become active")
	catalogClient, err := n.client.GetClusterCatalogClient(n.cluster.ID)
	require.NoError(n.T(), err)

	err = charts.WaitChartInstall(catalogClient, payload.Namespace, actionsCharts.NeuVectorChartName)
	require.NoError(n.T(), err)

	n.T().Log("Waiting for resources to become active")
	err = charts.WatchAndWaitDeployments(n.client, n.cluster.ID, payload.Namespace, metav1.ListOptions{})
	require.NoError(n.T(), err)

	err = charts.WatchAndWaitDaemonSets(n.client, n.cluster.ID, payload.Namespace, metav1.ListOptions{})
	require.NoError(n.T(), err)
}

func TestNeuVectorTestSuite(t *testing.T) {
	suite.Run(t, new(NeuVectorTestSuite))
}

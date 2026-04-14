//go:build (validation || infra.any || i.cluster.any || sanity || pit.daily || pit.elemental.daily) && !stress && !extended

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
	client              *rancher.Client
	session             *session.Session
	cluster             *clusters.ClusterMeta
	project             *management.Project
	chartInstallOptions *actionsCharts.PayloadOpts
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

	latestVersion, err := client.Catalog.GetLatestChartVersion(actionsCharts.NeuVectorChartName, catalog.RancherChartRepo)
	require.NoError(n.T(), err)

	n.chartInstallOptions = &actionsCharts.PayloadOpts{
		Namespace: actionsCharts.NeuVectorNamespace,
		InstallOptions: actionsCharts.InstallOptions{
			Cluster:   n.cluster,
			Version:   latestVersion,
			ProjectID: n.project.ID,
		},
	}
}

func (n *NeuVectorTestSuite) TestNeuVectorInstallation() {
	subSession := n.session.NewSession()
	defer subSession.Cleanup()

	client, err := n.client.WithSession(subSession)
	require.NoError(n.T(), err)

	n.T().Logf("Checking if NeuVector chart is already installed in namespace [%s]", actionsCharts.NeuVectorNamespace)
	neuVectorChartStatus, err := charts.GetChartStatus(client, n.cluster.ID, actionsCharts.NeuVectorNamespace, actionsCharts.NeuVectorChartName)
	require.NoError(n.T(), err)

	if !neuVectorChartStatus.IsAlreadyInstalled {
		n.T().Logf("Installing NeuVector on cluster [%s] with version [%s]", n.cluster.Name, n.chartInstallOptions.Version)
		err = actionsCharts.InstallNeuVectorChart(client, *n.chartInstallOptions)
		require.NoError(n.T(), err)

		n.T().Logf("Waiting for NeuVector chart to become active in namespace [%s]", actionsCharts.NeuVectorNamespace)
		catalogClient, err := client.GetClusterCatalogClient(n.cluster.ID)
		require.NoError(n.T(), err)

		err = charts.WaitChartInstall(catalogClient, actionsCharts.NeuVectorNamespace, actionsCharts.NeuVectorChartName)
		require.NoError(n.T(), err)
	}

	n.T().Log("Waiting for NeuVector Deployments to become ready")
	err = charts.WatchAndWaitDeployments(client, n.cluster.ID, actionsCharts.NeuVectorNamespace, metav1.ListOptions{})
	require.NoError(n.T(), err)

	n.T().Log("Waiting for NeuVector DaemonSets to become ready")
	err = charts.WatchAndWaitDaemonSets(client, n.cluster.ID, actionsCharts.NeuVectorNamespace, metav1.ListOptions{})
	require.NoError(n.T(), err)

	n.T().Log("Verifying all expected NeuVector services are active")
	err = verifyNeuVectorServicesActive(client, n.cluster.ID)
	require.NoError(n.T(), err)

	n.T().Log("Verifying NeuVector manager web UI is accessible via service proxy")
	err = verifyNeuVectorWebUIAccessible(client, n.cluster.ID)
	require.NoError(n.T(), err)
}

func TestNeuVectorTestSuite(t *testing.T) {
	suite.Run(t, new(NeuVectorTestSuite))
}

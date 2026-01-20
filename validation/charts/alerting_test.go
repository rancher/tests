//go:build (validation || infra.rke1 || cluster.any || stress || pit.daily) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !sanity && !extended

package charts

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/projects"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AlertingTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	project *management.Project
	cluster *clusters.ClusterMeta
}

func (i *AlertingTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *AlertingTestSuite) SetupSuite() {
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

	i.project, err = projects.GetProjectByName(i.client, clusterMeta.ID, charts.SystemProject)
	require.NoError(i.T(), err)
	require.Equal(i.T(), charts.SystemProject, i.project.Name)
}

func (i *AlertingTestSuite) TestAlertingInstallation() {
	latestAlertingVersion, err := i.client.Catalog.GetLatestChartVersion(
		charts.RancherAlertingName,
		catalog.RancherChartRepo,
	)
	require.NoError(i.T(), err)

	installOptions := &charts.InstallOptions{
		Cluster:   i.cluster,
		Version:   latestAlertingVersion,
		ProjectID: i.project.ID,
	}

	featureOptions := &charts.RancherAlertingOpts{
		SMS:   true,
		Teams: false,
	}

	i.T().Logf(
		"Installing Rancher Alerting chart on cluster [%s] with version [%s]",
		i.cluster.Name,
		latestAlertingVersion,
	)

	err = charts.InstallRancherAlertingChart(i.client, installOptions, featureOptions)
	require.NoError(i.T(), err)

	i.T().Logf(
		"Waiting for alerting deployments to become ready in namespace [%s]",
		charts.RancherMonitoringNamespace,
	)

	err = shepherdCharts.WatchAndWaitDeployments(
		i.client,
		i.cluster.ID,
		charts.RancherMonitoringNamespace,
		metav1.ListOptions{},
	)
	require.NoError(i.T(), err)
}

func TestAlertingTestSuite(t *testing.T) {
	suite.Run(t, new(AlertingTestSuite))
}

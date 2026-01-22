//go:build (validation || infra.any || cluster.any || sanity || pit.daily) && !stress && !extended

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

func (a *AlertingTestSuite) TearDownSuite() {
	a.session.Cleanup()
}

func (a *AlertingTestSuite) SetupSuite() {
	testSession := session.NewSession()
	a.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(a.T(), err)

	a.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(a.T(), clusterName, "Cluster name to install is not set")

	cluster, err := clusters.NewClusterMeta(client, clusterName)
	require.NoError(a.T(), err)
	a.cluster = cluster

	a.project, err = projects.GetProjectByName(a.client, cluster.ID, charts.SystemProject)
	require.NoError(a.T(), err)
	require.Equal(a.T(), charts.SystemProject, a.project.Name)
}

func (a *AlertingTestSuite) TestAlertingInstallation() {
	latestAlertingVersion, err := a.client.Catalog.GetLatestChartVersion(
		charts.RancherAlertingName,
		catalog.RancherChartRepo,
	)
	require.NoError(a.T(), err)

	installOptions := &charts.InstallOptions{
		Cluster:   a.cluster,
		Version:   latestAlertingVersion,
		ProjectID: a.project.ID,
	}

	featureOptions := &charts.RancherAlertingOpts{
		SMS:   true,
		Teams: false,
	}

	a.T().Logf(
		"Installing Rancher Alerting chart on cluster [%s] with version [%s]",
		a.cluster.Name,
		latestAlertingVersion,
	)

	err = charts.InstallRancherAlertingChart(a.client, installOptions, featureOptions)
	require.NoError(a.T(), err)

	a.T().Logf(
		"Waiting for alerting deployments to become ready in namespace [%s]",
		charts.RancherMonitoringNamespace,
	)

	err = shepherdCharts.WatchAndWaitDeployments(
		a.client,
		a.cluster.ID,
		charts.RancherMonitoringNamespace,
		metav1.ListOptions{},
	)
	require.NoError(a.T(), err)
}

func TestAlertingTestSuite(t *testing.T) {
	suite.Run(t, new(AlertingTestSuite))
}

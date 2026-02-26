//go:build (validation || infra.any || i.cluster.any || sanity || pit.daily) && !stress && !extended

package charts

import (
	"context"
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"

	"github.com/rancher/shepherd/extensions/charts"
	interoperablecharts "github.com/rancher/tests/interoperability/charts"
	qaconfig "github.com/rancher/tests/interoperability/qainfraautomation/config"
	"github.com/sirupsen/logrus"

	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	actionsCharts "github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/uiplugins"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/interoperability/qainfraautomation"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	uiPluginChartsRepoName = "rancher-ui-plugins"
	uiPluginChartsURL      = "https://github.com/rancher/ui-plugin-charts"
	uiPluginChartsBranch   = "main"
)

type NeuVectorHardenedTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
	project *management.Project
}

func (n *NeuVectorHardenedTestSuite) TearDownSuite() {
	n.session.Cleanup()
}

func (n *NeuVectorHardenedTestSuite) SetupSuite() {
	testSession := session.NewSession()
	n.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(n.T(), err)

	n.client = client


	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	
	cfg := new(qaconfig.Config)
	operations.LoadObjectFromMap(qaconfig.ConfigurationFileKey, cattleConfig, cfg)

	clusterObj, cleanup, err := qainfraautomation.ProvisionRancherCluster(
		n.client,
		cfg,
		cfg.RancherCluster,
	)
	require.NoError(n.T(), err)
	n.session.RegisterCleanupFunc(cleanup)


	n.T().Logf("Verifying the cluster is ready (%s)", clusterObj.Name)
	err = provisioning.VerifyClusterReady(n.client, clusterObj)
	require.NoError(n.T(), err)

	logrus.Infof("Verifying cluster deployments (%s)", clusterObj.Name)
	err = deployment.VerifyClusterDeployments(n.client, clusterObj)
	require.NoError(n.T(), err)

	logrus.Infof("Verifying cluster pods (%s)", clusterObj.Name)
	err = pods.VerifyClusterPods(n.client, clusterObj)
	require.NoError(n.T(), err)

	require.NotNil(n.T(), clusterObj, "expected a non-nil cluster object")
	n.T().Logf("cluster %q is ready", clusterObj.Name)


	cluster, err := clusters.NewClusterMeta(client, clusterObj.Name)
	require.NoError(n.T(), err)

	n.cluster = cluster

	n.T().Logf("Creating Project [%s]", actionsCharts.SystemProject)
	n.project, err = projects.GetProjectByName(n.client, cluster.ID, actionsCharts.SystemProject)
	require.NoError(n.T(), err)
	require.Equal(n.T(), actionsCharts.SystemProject, n.project.Name)

	_, err = n.client.Catalog.ClusterRepos().Get(context.TODO(), uiPluginChartsRepoName, metav1.GetOptions{})
	if k8sErrors.IsNotFound(err) {
		logrus.Infof("UI plugin repo %q not found, creating it", uiPluginChartsRepoName)
		err = uiplugins.CreateExtensionsRepo(n.client, uiPluginChartsRepoName, uiPluginChartsURL, uiPluginChartsBranch)
	}
	
	require.NoError(n.T(), err)


	n.T().Logf("Checking if NeuVector UI extension [%s] is already installed", interoperablecharts.NeuVectorUIExtensionName)
	uiExtensionObj, err := charts.GetChartStatus(n.client, "local", interoperablecharts.ExtensionNamespace, interoperablecharts.NeuVectorUIExtensionName)
	require.NoError(n.T(), err)

	if !uiExtensionObj.IsAlreadyInstalled {
		n.T().Logf("Getting the latest chart version for [%s]", interoperablecharts.NeuVectorUIExtensionName)
		latestVersion, err := n.client.Catalog.GetLatestChartVersion(interoperablecharts.NeuVectorUIExtensionName, uiPluginChartsRepoName)
		require.NoError(n.T(), err)

		extensionOptions := &uiplugins.ExtensionOptions{
			ChartName:   interoperablecharts.NeuVectorUIExtensionName,
			ReleaseName: interoperablecharts.NeuVectorUIExtensionName,
			Version:     latestVersion,
		}

		n.T().Log("Installing NeuVector UI extension on local cluster")
		err = uiplugins.InstallUIPlugin(n.client, extensionOptions, uiPluginChartsRepoName)
		require.NoError(n.T(), err)
	}
}

func (n *NeuVectorHardenedTestSuite) TestNeuVectorInstallation() {
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
	err = actionsCharts.InstallLatestNeuVectorChart(n.client, payload)
	require.NoError(n.T(), err)

	n.T().Log("Waiting for NeuVector chart to become active")
	catalogClient, err := n.client.GetClusterCatalogClient(n.cluster.ID)
	require.NoError(n.T(), err)

	err = actionsCharts.WaitChartDeployed(catalogClient, payload.Namespace, actionsCharts.NeuVectorChartName)
	require.NoError(n.T(), err)

	n.T().Log("Waiting for resources to become active")
	err = charts.WatchAndWaitDeployments(n.client, n.cluster.ID, payload.Namespace, metav1.ListOptions{})
	require.NoError(n.T(), err)

	err = charts.WatchAndWaitDaemonSets(n.client, n.cluster.ID, payload.Namespace, metav1.ListOptions{})
	require.NoError(n.T(), err)
}

func TestNeuVectorHardenedTestSuite(t *testing.T) {
	suite.Run(t, new(NeuVectorHardenedTestSuite))
}

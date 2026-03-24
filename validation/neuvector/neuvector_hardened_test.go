//go:build (validation || infra.any || i.cluster.any || sanity || pit.daily) && !stress && !extended

package charts

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"

	"github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/ingresses"
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
	cfg     *qaconfig.Config
	cluster *v1.SteveAPIObject
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

	n.cfg = new(qaconfig.Config)
	operations.LoadObjectFromMap(qaconfig.ConfigurationFileKey, cattleConfig, n.cfg)

	require.NotNil(n.T(), n.cfg.CustomCluster, "customCluster config is required under qaInfraAutomation.customCluster")
	n.cfg.CustomCluster.Harden = true

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

	clusterObj := qainfraautomation.ProvisionCustomCluster(
		n.T(),
		n.client,
		n.cfg,
		n.cfg.CustomCluster,
	)

	require.NotNil(n.T(), clusterObj, "expected a non-nil cluster object")
	n.T().Logf("cluster %q is ready", clusterObj.Name)

	logrus.Infof("Verifying the cluster is ready (%s)", clusterObj.Name)
	err = provisioning.VerifyClusterReady(n.client, clusterObj)
	require.NoError(n.T(), err)

	logrus.Infof("Verifying cluster deployments (%s)", clusterObj.Name)
	err = deployment.VerifyClusterDeployments(n.client, clusterObj)
	require.NoError(n.T(), err)

	logrus.Infof("Verifying cluster pods (%s)", clusterObj.Name)
	err = pods.VerifyClusterPods(n.client, clusterObj)
	require.NoError(n.T(), err)

	n.cluster = clusterObj
}

func (n *NeuVectorHardenedTestSuite) TestNeuVectorInstallation() {
	cluster, err := clusters.NewClusterMeta(n.client, n.cluster.Name)
	require.NoError(n.T(), err)

	n.T().Logf("Fetching Project [%s]", actionsCharts.SystemProject)
	project, err := projects.GetProjectByName(n.client, cluster.ID, actionsCharts.SystemProject)
	require.NoError(n.T(), err)
	require.Equal(n.T(), actionsCharts.SystemProject, project.Name)

	n.T().Logf("Getting the latest chart version for [%s]", actionsCharts.NeuVectorChartName)
	latestVersions, err := n.client.Catalog.GetListChartVersions(actionsCharts.NeuVectorChartName, catalog.RancherChartRepo)
	require.NoError(n.T(), err)

	payload := actionsCharts.PayloadOpts{
		Namespace: actionsCharts.NeuVectorNamespace,
		Host:      n.client.RancherConfig.Host,
		InstallOptions: actionsCharts.InstallOptions{
			Cluster:   cluster,
			Version:   latestVersions[0],
			ProjectID: project.ID,
		},
		K3s:      strings.Contains(n.cfg.CustomCluster.KubernetesVersion, "k3s"),
		Hardened: true,
	}

	n.T().Logf("Installing NeuVector %s on cluster %s", latestVersions[0], cluster.Name)
	err = actionsCharts.InstallNeuVectorChart(n.client, payload)
	require.NoError(n.T(), err)

	n.T().Log("Waiting for NeuVector chart to become active")
	catalogClient, err := n.client.GetClusterCatalogClient(cluster.ID)
	require.NoError(n.T(), err)
	err = charts.WaitChartInstall(catalogClient, payload.Namespace, actionsCharts.NeuVectorChartName)
	require.NoError(n.T(), err)

	n.T().Log("Waiting for resources to become active")
	err = charts.WatchAndWaitDeployments(n.client, cluster.ID, payload.Namespace, metav1.ListOptions{})
	require.NoError(n.T(), err)

	err = charts.WatchAndWaitDaemonSets(n.client, cluster.ID, payload.Namespace, metav1.ListOptions{})
	require.NoError(n.T(), err)

	n.T().Log("Verifying NeuVector manager UI is reachable via service proxy")
	uiProxyPath := fmt.Sprintf(
		"k8s/clusters/%s/api/v1/namespaces/%s/services/https:neuvector-service-webui:8443/proxy/",
		cluster.ID,
		actionsCharts.NeuVectorNamespace,
	)
	_, err = ingresses.GetExternalIngressResponse(n.client, n.client.RancherConfig.Host, uiProxyPath, true)
	require.NoError(n.T(), err, "NeuVector manager UI should be reachable via service proxy")
}

func TestNeuVectorHardenedTestSuite(t *testing.T) {
	suite.Run(t, new(NeuVectorHardenedTestSuite))
}

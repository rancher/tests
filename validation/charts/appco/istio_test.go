package appco

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	kubeapinamespaces "github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type IstioTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *clusters.ClusterMeta
}

func (i *IstioTestSuite) TearDownSuite() {
	i.session.Cleanup()
}

func (i *IstioTestSuite) SetupSuite() {
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
}

func (i *IstioTestSuite) TestSideCarInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = kubeapinamespaces.CreateNamespace(client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecretName))

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestAmbientInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = kubeapinamespaces.CreateNamespace(client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecretName))

	i.T().Log("Installing Ambient Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, IstioAmbientModeSet)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestGatewayStandaloneInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = kubeapinamespaces.CreateNamespace(client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecretName))

	i.T().Log("Installing Gateway Standalone Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, IstioGatewayModeSet)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestGatewayDiffNamespaceInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = kubeapinamespaces.CreateNamespace(client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecretName))

	i.T().Log("Installing Gateway Namespace Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, IstioGatewayDiffNamespaceModeSet)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestInstallWithCanaryUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = kubeapinamespaces.CreateNamespace(client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecretName))

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running Canary Istio AppCo Upgrade")
	istioChart, logCmd, err = watchAndwaitUpgradeIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, IstioCanaryUpgradeSet)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Verifying if istio-ingress gateway is using the canary revision")
	logCmd, err = verifyCanaryRevision(client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, IstioCanaryRevisionApp))
}

func (i *IstioTestSuite) TestInPlaceUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = kubeapinamespaces.CreateNamespace(client, i.cluster.ID, namegen.AppendRandomString("testns"), charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{})
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecretName))

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running In Place Istio AppCo Upgrade")
	istioChart, logCmd, err = watchAndwaitUpgradeIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, ExpectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func TestIstioTestSuite(t *testing.T) {
	suite.Run(t, new(IstioTestSuite))
}

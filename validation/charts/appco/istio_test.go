package appco

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	expectedDeployLog = "deployed"
	canaryRevisionApp = "istiod-canary"
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

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	err = createIstioNamespace(i.client, i.cluster.ID)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecret)
	logCmd, err := createIstioSecret(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecret))

	i.T().Log("Creating pilot job")
	err = createPilotJob(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
}

func (i *IstioTestSuite) TestSideCarInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := installIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = uninstallIstioAppCo(client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestAmbientInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	ambientSets := `--set cni.enabled=true,ztunnel.enabled=true --set istiod.cni.enabled=false --set cni.profile=ambient,istiod.profile=ambient,ztunnel.profile=ambient`

	i.T().Log("Installing Ambient Istio AppCo")
	istioChart, logCmd, err := installIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, ambientSets)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = uninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestGatewayStandaloneInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	gatewaySets := `--set base.enabled=false,istiod.enabled=false --set gateway.enabled=true`

	i.T().Log("Installing Gateway Standalone Istio AppCo")
	istioChart, logCmd, err := installIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, gatewaySets)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = uninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestGatewayDiffNamespaceInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	gatewayNamespaceSets := `--set gateway.enabled=true,gateway.namespaceOverride=default`

	i.T().Log("Installing Gateway Namespace Istio AppCo")
	istioChart, logCmd, err := installIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, gatewayNamespaceSets)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = uninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestCanaryUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := installIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running Canary Istio AppCo Upgrade")
	istioChart, logCmd, err = upgradeIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "--set istiod.revision=canary,base.defaultRevision=canary")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Verifying if istio-ingress gateway is using the canary revision")
	getCanaryCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`kubectl -n %s get pod -o jsonpath='{.items..metadata.name}'`, charts.RancherIstioNamespace),
	}

	appName := canaryRevisionApp
	logCmd, err = kubectl.Command(client, nil, i.cluster.ID, getCanaryCommand, "2MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, appName))

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = uninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)

}

func (i *IstioTestSuite) TestInPlaceUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := installIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running In Place Istio AppCo Upgrade")
	istioChart, logCmd, err = upgradeIstioAppCo(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = uninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func TestIstioTestSuite(t *testing.T) {
	suite.Run(t, new(IstioTestSuite))
}

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
	//Get it from env var
	username = ""
	//Get it from env var
	accessToken = ""
	pilotImage  = "dp.apps.rancher.io/containers/pilot:1.25.3"
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
	err = CreateIstioNamespace(i.client, i.cluster.ID)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", RancherIstioSecret)
	logCmd, err := CreateIstioSecret(i.client, i.cluster.ID, username, accessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, RancherIstioSecret))

	i.T().Log("Creating pilot job")
	err = CreatePilotJob(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
}

func (i *IstioTestSuite) TestIstioSideCarInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := InstallIstioAppCo(client, i.cluster.ID, username, accessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = UninstallIstioAppCo(client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioAmbientInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	ambientSets := `--set cni.enabled=true,ztunnel.enabled=true --set istiod.cni.enabled=true --set cni.profile=ambient,istiod.profile=ambient,ztunnel.profile=ambient`

	i.T().Log("Installing Ambient Istio AppCo")
	istioChart, logCmd, err := InstallIstioAppCo(client, i.cluster.ID, username, accessToken, ambientSets)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = UninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioGatewayStandaloneInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	gatewaySets := `--set base.enabled=false,istiod.enabled=false --set gateway.enabled=true`

	i.T().Log("Installing Gateway Standalone Istio AppCo")
	istioChart, logCmd, err := InstallIstioAppCo(client, i.cluster.ID, username, accessToken, gatewaySets)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = UninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioGatewayDiffNamespaceInstallation() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	gatewayNamespaceSets := `--set gateway.enabled=true,gateway.namespaceOverride=default`

	i.T().Log("Installing Gateway Namespace Istio AppCo")
	istioChart, logCmd, err := InstallIstioAppCo(client, i.cluster.ID, username, accessToken, gatewayNamespaceSets)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = UninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestIstioCanaryUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := InstallIstioAppCo(client, i.cluster.ID, username, accessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running Canary Istio AppCo Upgrade")
	istioChart, logCmd, err = UpgradeIstioAppCo(client, i.cluster.ID, username, accessToken, "--set istiod.revision=canary,base.defaultRevision=canary")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Verifying if istio-ingress gateway is using the canary revision")
	getCanaryCommand := []string{
		"sh", "-c",
		fmt.Sprintf(`kubectl -n %s get pod -o jsonpath='{.items..metadata.name}'`, charts.RancherIstioNamespace),
	}

	appName := "istiod-canary"
	logCmd, err = kubectl.Command(client, nil, i.cluster.ID, getCanaryCommand, "2MB")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, appName))

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = UninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)

}

func (i *IstioTestSuite) TestIstioInPlaceUpgrade() {
	subSession := i.session.NewSession()
	defer subSession.Cleanup()

	client, err := i.client.WithSession(subSession)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := InstallIstioAppCo(client, i.cluster.ID, username, accessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running In Place Istio AppCo Upgrade")
	istioChart, logCmd, err = UpgradeIstioAppCo(client, i.cluster.ID, username, accessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Uninstalling Istio AppCo")
	istioChart, err = UninstallIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.False(i.T(), istioChart.IsAlreadyInstalled)
}

func TestIstioTestSuite(t *testing.T) {
	suite.Run(t, new(IstioTestSuite))
}

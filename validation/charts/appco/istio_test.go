//go:build (validation || infra.any || cluster.any || extended || pit.daily) && !sanity && !stress

package appco

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	extensionClusters "github.com/rancher/shepherd/extensions/clusters"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	namespaces "github.com/rancher/tests/actions/namespaces"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type IstioTestSuite struct {
	suite.Suite
	client      *rancher.Client
	session     *session.Session
	cluster     *clusters.ClusterMeta
	clusterName string
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

	provisioningClusterID, err := extensionClusters.GetV1ProvisioningClusterByName(client, clusterName)
	require.NoError(i.T(), err)

	steveCluster, err := client.Steve.SteveType(extensionClusters.ProvisioningSteveResourceType).ByID(provisioningClusterID)
	require.NoError(i.T(), err)

	newCluster := &provv1.Cluster{}
	err = steveV1.ConvertToK8sType(steveCluster, newCluster)
	require.NoError(i.T(), err)

	i.clusterName = client.RancherConfig.ClusterName
	if !strings.Contains(newCluster.Spec.KubernetesVersion, "k3s") && !strings.Contains(newCluster.Spec.KubernetesVersion, "rke2") {
		i.clusterName = i.cluster.ID
	}
}

func (i *IstioTestSuite) TearDownTest() {
	_, err := charts.DeleteIstioResources(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	i.session.Cleanup()
}

func (i *IstioTestSuite) SetupTest() {
	testSession := session.NewSession()
	i.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(i.T(), err)

	i.client = client

	projectConfig := &management.Project{
		ClusterID: i.cluster.ID,
		Name:      exampleAppProjectName,
	}
	project, err := client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), project.Name, exampleAppProjectName)

	i.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = namespaces.CreateNamespace(client, charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{}, project)
	require.NoError(i.T(), err)

	i.T().Logf("Creating %s secret", rancherIstioSecretName)
	logCmd, err := createIstioSecret(client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, rancherIstioSecretName))
}

func (i *IstioTestSuite) TestSideCarInstallation() {
	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestAmbientInstallation() {
	i.T().Log("Installing Ambient Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, istioAmbientModeSet)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, "deployed"))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestGatewayStandaloneInstallation() {
	projectName := namegenerator.AppendRandomString("project-name")
	projectConfig := &management.Project{
		ClusterID: i.cluster.ID,
		Name:      projectName,
	}
	project, err := i.client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), project.Name, projectName)

	diffNamespace := namegenerator.AppendRandomString("diff-namespace")
	i.T().Logf("Creating %s namespace", diffNamespace)
	_, err = namespaces.CreateNamespace(i.client, diffNamespace, "{}", map[string]string{}, map[string]string{}, project)
	require.NoError(i.T(), err)

	i.T().Log("Installing Gateway Standalone Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, fmt.Sprintf(istioGatewayModeSet, diffNamespace))
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestGatewayDiffNamespaceInstallation() {
	projectName := namegenerator.AppendRandomString("project-name")
	projectConfig := &management.Project{
		ClusterID: i.cluster.ID,
		Name:      projectName,
	}
	project, err := i.client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), project.Name, projectName)

	diffNamespace := namegenerator.AppendRandomString("diff-namespace")
	i.T().Logf("Creating %s namespace", diffNamespace)
	_, err = namespaces.CreateNamespace(i.client, diffNamespace, "{}", map[string]string{}, map[string]string{}, project)
	require.NoError(i.T(), err)

	i.T().Log("Installing Gateway Namespace Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, fmt.Sprintf(istioGatewayDiffNamespaceModeSet, diffNamespace))
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestInstallWithCanaryUpgrade() {
	projectName := namegenerator.AppendRandomString("project-name")
	projectConfig := &management.Project{
		ClusterID: i.cluster.ID,
		Name:      projectName,
	}
	project, err := i.client.Management.Project.Create(projectConfig)
	require.NoError(i.T(), err)
	require.Equal(i.T(), project.Name, projectName)

	diffNamespace := namegenerator.AppendRandomString("diff-namespace")
	i.T().Logf("Creating %s namespace", diffNamespace)
	_, err = namespaces.CreateNamespace(i.client, diffNamespace, "{}", map[string]string{}, map[string]string{}, project)
	require.NoError(i.T(), err)

	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running Canary Istio AppCo Upgrade")
	istioChart, logCmd, err = watchAndwaitUpgradeIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, fmt.Sprintf(istioCanaryUpgradeSet, diffNamespace))
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Verifying if istio-ingress gateway is using the canary revision")
	logCmd, err = verifyCanaryRevision(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, istioCanaryRevisionApp))
}

func (i *IstioTestSuite) TestInPlaceUpgrade() {
	i.T().Log("Installing SideCar Istio AppCo")
	istioChart, logCmd, err := watchAndwaitInstallIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)

	i.T().Log("Running In Place Istio AppCo Upgrade")
	istioChart, logCmd, err = watchAndwaitUpgradeIstioAppCo(i.client, i.cluster.ID, *AppCoUsername, *AppCoAccessToken, "")
	require.NoError(i.T(), err)
	require.True(i.T(), strings.Contains(logCmd, expectedDeployLog))
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func (i *IstioTestSuite) TestFleetInstallation() {
	i.T().Log("Creating Fleet repo")
	repoObject, err := watchAndwaitCreateFleetGitRepo(i.client, i.clusterName, i.cluster.ID)
	require.NoError(i.T(), err)

	log.Info("Getting GitRepoStatus")
	gitRepo, err := i.client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(repoObject.ID)
	require.NoError(i.T(), err)

	gitStatus := &v1alpha1.GitRepoStatus{}
	err = steveV1.ConvertToK8sType(gitRepo.Status, gitStatus)
	require.NoError(i.T(), err)

	istioChart, err := watchAndwaitIstioAppCo(i.client, i.cluster.ID)
	require.NoError(i.T(), err)
	require.True(i.T(), istioChart.IsAlreadyInstalled)
}

func TestIstioTestSuite(t *testing.T) {
	suite.Run(t, new(IstioTestSuite))
}

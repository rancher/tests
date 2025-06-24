package appco

import (
	"strings"
	"testing"

	"github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionClusters "github.com/rancher/shepherd/extensions/clusters"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	namespaces "github.com/rancher/tests/actions/namespaces"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type FleetTestSuite struct {
	suite.Suite
	client      *rancher.Client
	session     *session.Session
	cluster     *management.Cluster
	clusterName string
	project     *management.Project
}

func (u *FleetTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *FleetTestSuite) TearDownTest() {
	u.session.Cleanup()
}

func (u *FleetTestSuite) SetupSuite() {
	u.session = session.NewSession()

	client, err := rancher.NewClient("", u.session)
	require.NoError(u.T(), err)

	u.client = client

	log.Info("Getting cluster name from the config file and append cluster details in connection")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(u.T(), clusterName, "Cluster name to install should be set")

	clusterID, err := extensionClusters.GetClusterIDByName(u.client, clusterName)
	require.NoError(u.T(), err, "Error getting cluster ID")

	u.cluster, err = u.client.Management.Cluster.ByID(clusterID)
	require.NoError(u.T(), err)

	provisioningClusterID, err := extensionClusters.GetV1ProvisioningClusterByName(client, clusterName)
	require.NoError(u.T(), err)

	cluster, err := client.Steve.SteveType(extensionClusters.ProvisioningSteveResourceType).ByID(provisioningClusterID)
	require.NoError(u.T(), err)

	newCluster := &provv1.Cluster{}
	err = steveV1.ConvertToK8sType(cluster, newCluster)
	require.NoError(u.T(), err)

	u.clusterName = client.RancherConfig.ClusterName
	if !strings.Contains(newCluster.Spec.KubernetesVersion, "k3s") && !strings.Contains(newCluster.Spec.KubernetesVersion, "rke2") {
		u.clusterName = u.cluster.ID
	}

	projectConfig := &management.Project{
		ClusterID: clusterID,
		Name:      exampleAppProjectName,
	}
	createdProject, err := client.Management.Project.Create(projectConfig)
	require.NoError(u.T(), err)
	require.Equal(u.T(), createdProject.Name, exampleAppProjectName)
	u.project = createdProject
}

func (u *FleetTestSuite) SetupTest() {
	testSession := session.NewSession()
	u.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(u.T(), err)

	u.client = client

	u.T().Logf("Creating %s namespace", charts.RancherIstioNamespace)
	_, err = namespaces.CreateNamespace(client, charts.RancherIstioNamespace, "{}", map[string]string{}, map[string]string{}, u.project)
	require.NoError(u.T(), err)

	u.T().Logf("Creating %s secret", rancherIstioSecretName)
	logCmd, err := createIstioSecret(client, u.cluster.ID, *AppCoUsername, *AppCoAccessToken)
	require.NoError(u.T(), err)
	require.True(u.T(), strings.Contains(logCmd, rancherIstioSecretName))
}

func (u *FleetTestSuite) TestIstioInstallation() {
	log.Info("Creating Fleet repo")
	repoObject, err := createFleetGitRepo(u.client, u.clusterName, u.cluster.ID)
	require.NoError(u.T(), err)

	log.Info("Getting GitRepoStatus")
	gitRepo, err := u.client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(repoObject.ID)
	require.NoError(u.T(), err)

	gitStatus := &v1alpha1.GitRepoStatus{}
	err = steveV1.ConvertToK8sType(gitRepo.Status, gitStatus)
	require.NoError(u.T(), err)

	istioChart, err := watchAndwaitIstioAppCo(u.client, u.cluster.ID)
	require.NoError(u.T(), err)
	require.True(u.T(), istioChart.IsAlreadyInstalled)
}

func TestFleetTestSuite(t *testing.T) {
	suite.Run(t, new(FleetTestSuite))
}

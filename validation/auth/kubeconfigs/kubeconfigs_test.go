//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !(2.8 || 2.9 || 2.10 || 2.11)

package kubeconfigs

import (
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigapi "github.com/rancher/tests/actions/kubeconfigs"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KubeconfigTestSuite struct {
	suite.Suite
	client     *rancher.Client
	session    *session.Session
	cluster    *management.Cluster
	aceCluster *management.Cluster
}

func (kc *KubeconfigTestSuite) SetupSuite() {
	err := os.Setenv("DISABLE_PROTOBUF", "true")
	require.NoError(kc.T(), err)

	kc.session = session.NewSession()

	client, err := rancher.NewClient("", kc.session)
	require.NoError(kc.T(), err)
	kc.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rbos")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(kc.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(kc.client, clusterName)
	require.NoError(kc.T(), err, "Error getting cluster ID")
	kc.cluster, err = kc.client.Management.Cluster.ByID(clusterID)
	require.NoError(kc.T(), err)

	log.Infof("Creating a ACE enabled cluster")
	clusterObject, clusterConfig, err := kubeconfigapi.CreateDownstreamACEEnabledCluster(kc.client)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), clusterObject)
	require.NotNil(kc.T(), clusterConfig)
	aceClusterID, err := clusters.GetClusterIDByName(kc.client, clusterObject.Name)
	require.NoError(kc.T(), err)
	provisioning.VerifyCluster(kc.T(), kc.client, clusterConfig, clusterObject)
	kc.aceCluster, err = kc.client.Management.Cluster.ByID(aceClusterID)
	require.NoError(kc.T(), err)
	log.Infof("ACE-enabled cluster created: %s (%s)", kc.aceCluster.Name, aceClusterID)
}

func (kc *KubeconfigTestSuite) TearDownSuite() {
	if kc.aceCluster != nil {
		if err := kubeconfigapi.DeleteClusterAndWait(kc.client, kc.aceCluster); err != nil {
			log.Errorf("Failed to delete ACE cluster: %v", err)
		}
	}
	kc.session.Cleanup()
}

func (kc *KubeconfigTestSuite) validateKubeconfigAndBackingResources(client *rancher.Client, userClient *rancher.Client, kubeconfigName string, expectedClusters []string, expectedUserID string,
	expectedCurrentContext string, expectedTTL int64, clusterType string) {

	log.Infof("GET the kubeconfig to validate the fields")
	kubeconfigObj, err := kubeconfigapi.GetKubeconfig(client, kubeconfigName)
	require.NoError(kc.T(), err)

	log.Infof("Validating kubeconfig has the label cattle.io/user-id and it matches the expected user ID: %s", expectedUserID)
	userID, ok := kubeconfigObj.Labels[kubeconfigapi.UserIDLabel]
	require.True(kc.T(), ok, "Expected label cattle.io/user-id to exist on kubeconfig")
	require.Equal(kc.T(), expectedUserID, userID, "Label cattle.io/user-id should match the creator's user ID")

	log.Infof("Validating the kubeconfig spec fields: clusters, currentContext, and TTL")
	err = kubeconfigapi.VerifyKubeconfigSpec(kubeconfigObj, expectedClusters, expectedCurrentContext, expectedTTL, clusterType)
	require.NoError(kc.T(), err, "Kubeconfig spec validation failed")

	log.Infof("Validating status summary is Complete")
	require.Equal(kc.T(), kubeconfigapi.StatusCompletedSummary, kubeconfigObj.Status.Summary)

	log.Infof("Validating tokens and owner references")
	err = kubeconfigapi.VerifyKubeconfigTokens(client, kubeconfigObj, clusterType)
	require.NoError(kc.T(), err)

	log.Infof("Validating backing tokens are created for kubeconfig %q", kubeconfigName)
	tokens, err := kubeconfigapi.GetBackingTokensForKubeconfigName(userClient, kubeconfigName)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens, "Expected at least one backing token for kubeconfig")

	expectedTokenCount := 1
	if strings.ToLower(clusterType) == kubeconfigapi.AceClusterType {
		expectedTokenCount = len(expectedClusters) + 1
	}
	require.Equal(kc.T(), expectedTokenCount, len(tokens),
		"Expected %d backing tokens for cluster type %s, got %d. Kubeconfig has: %s",
		expectedTokenCount, clusterType, len(tokens), kubeconfigName)

	log.Infof("Validating backing ConfigMap is created for kubeconfig %q", kubeconfigName)
	backingConfigMap, err := client.WranglerContext.Core.ConfigMap().List(kubeconfigapi.KubeconfigConfigmapNamespace, metav1.ListOptions{
		FieldSelector: "metadata.name=" + kubeconfigName,
	})
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), 1, len(backingConfigMap.Items))
}

func (kc *KubeconfigTestSuite) TestCreateKubeconfigAsAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for cluster: %s", rbac.Admin.String(), kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	kubeconfigFile := "kc_config.yaml"
	err = os.WriteFile(kubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigFile)

	log.Infof("Validating the kubeconfig content")
	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigFile, []string{kc.cluster.ID}, kc.client.RancherConfig.Host, false, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := kubeconfigapi.GetKubeconfigDefaultTTLMinutes(kc.client)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.cluster.ID}, userID, kc.cluster.ID, expectedTTL, kubeconfigapi.NonAceClusterType)
}

func (kc *KubeconfigTestSuite) TestCreateKubeconfigAsClusterOwner() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Create a user and add the user to the downstream cluster with role %s", rbac.ClusterOwner.String())
	createdUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", createdUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(standardUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	kubeconfigFile := "ds_config.yaml"
	err = os.WriteFile(kubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigFile)

	log.Infof("Validating the kubeconfig content")
	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigFile, []string{kc.cluster.ID}, kc.client.RancherConfig.Host, false, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := kubeconfigapi.GetKubeconfigDefaultTTLMinutes(kc.client)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	kc.validateKubeconfigAndBackingResources(kc.client, standardUserClient, createdKubeconfig.Name,
		[]string{kc.cluster.ID}, createdUser.ID, kc.cluster.ID, expectedTTL, kubeconfigapi.NonAceClusterType)
}

func (kc *KubeconfigTestSuite) TestCreateKubeconfigAsAdminForAceCluster() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for the ACE cluster: %s", rbac.Admin.String(), kc.aceCluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.aceCluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	kubeconfigFile := "kc_ace_config.yaml"
	err = os.WriteFile(kubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigFile)

	log.Infof("Validating the kubeconfig content")
	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigFile, []string{kc.aceCluster.ID}, kc.client.RancherConfig.Host, true, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := kubeconfigapi.GetKubeconfigDefaultTTLMinutes(kc.client)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.aceCluster.ID}, userID, kc.aceCluster.ID, expectedTTL, kubeconfigapi.AceClusterType)
}

func TestKubeconfigTestSuite(t *testing.T) {
	suite.Run(t, new(KubeconfigTestSuite))
}

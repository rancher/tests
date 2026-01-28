//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

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
	"github.com/rancher/tests/actions/settings"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ExtKubeconfigTestSuite struct {
	suite.Suite
	client      *rancher.Client
	session     *session.Session
	cluster     *management.Cluster
	aceCluster1 *management.Cluster
	aceCluster2 *management.Cluster
	cluster2    *management.Cluster
}

func (kc *ExtKubeconfigTestSuite) SetupSuite() {
	kc.session = session.NewSession()

	client, err := rancher.NewClient("", kc.session)
	require.NoError(kc.T(), err)
	kc.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(kc.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(kc.client, clusterName)
	require.NoError(kc.T(), err, "Error getting cluster ID")
	kc.cluster, err = kc.client.Management.Cluster.ByID(clusterID)
	require.NoError(kc.T(), err)

	log.Infof("Creating additional clusters - ACE-enabled and non-ACE clusters")
	aceClusterObject1, aceClusterConfig1, err := kubeconfigapi.CreateDownstreamCluster(kc.client, true)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), aceClusterObject1)
	require.NotNil(kc.T(), aceClusterConfig1)
	aceCluster1ID, err := clusters.GetClusterIDByName(kc.client, aceClusterObject1.Name)
	require.NoError(kc.T(), err)

	aceClusterObject2, aceClusterConfig2, err := kubeconfigapi.CreateDownstreamCluster(kc.client, true)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), aceClusterObject2)
	require.NotNil(kc.T(), aceClusterConfig2)
	aceCluster2ID, err := clusters.GetClusterIDByName(kc.client, aceClusterObject2.Name)
	require.NoError(kc.T(), err)

	clusterObject2, clusterConfig2, err := kubeconfigapi.CreateDownstreamCluster(kc.client, false)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), clusterObject2)
	require.NotNil(kc.T(), clusterConfig2)
	cluster2ID, err := clusters.GetClusterIDByName(kc.client, clusterObject2.Name)
	require.NoError(kc.T(), err)

	err = provisioning.VerifyClusterReady(client, aceClusterObject1)
	require.NoError(kc.T(), err)
	err = provisioning.VerifyClusterReady(client, aceClusterObject2)
	require.NoError(kc.T(), err)
	err = provisioning.VerifyClusterReady(client, clusterObject2)
	require.NoError(kc.T(), err)

	err = deployment.VerifyClusterDeployments(client, aceClusterObject1)
	require.NoError(kc.T(), err)
	err = deployment.VerifyClusterDeployments(client, aceClusterObject2)
	require.NoError(kc.T(), err)
	err = deployment.VerifyClusterDeployments(client, clusterObject2)
	require.NoError(kc.T(), err)

	err = pods.VerifyClusterPods(client, aceClusterObject1)
	require.NoError(kc.T(), err)
	err = pods.VerifyClusterPods(client, aceClusterObject2)
	require.NoError(kc.T(), err)
	err = pods.VerifyClusterPods(client, clusterObject2)
	require.NoError(kc.T(), err)

	provisioning.VerifyDynamicCluster(kc.T(), client, aceClusterObject1)
	provisioning.VerifyDynamicCluster(kc.T(), client, aceClusterObject2)
	provisioning.VerifyDynamicCluster(kc.T(), client, clusterObject2)

	kc.aceCluster1, err = kc.client.Management.Cluster.ByID(aceCluster1ID)
	require.NoError(kc.T(), err)
	log.Infof("ACE-enabled cluster created: %s (%s)", kc.aceCluster1.Name, aceCluster1ID)
	kc.aceCluster2, err = kc.client.Management.Cluster.ByID(aceCluster2ID)
	require.NoError(kc.T(), err)
	log.Infof("ACE-enabled cluster created: %s (%s)", kc.aceCluster2.Name, aceCluster2ID)
	kc.cluster2, err = kc.client.Management.Cluster.ByID(cluster2ID)
	require.NoError(kc.T(), err)
	log.Infof("ACE-disabled cluster created: %s (%s)", kc.cluster2.Name, cluster2ID)
}

func (kc *ExtKubeconfigTestSuite) TearDownSuite() {
	kc.session.Cleanup()
}

func (kc *ExtKubeconfigTestSuite) validateKubeconfigAndBackingResources(client *rancher.Client, userClient *rancher.Client, kubeconfigName string, expectedClusters []string, expectedUserID string,
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
	log.Infof("Number of backing tokens: %d", len(tokens))

	log.Infof("Validating backing ConfigMap is created for kubeconfig %q", kubeconfigName)
	backingConfigMap, err := client.WranglerContext.Core.ConfigMap().List(kubeconfigapi.KubeconfigConfigmapNamespace, metav1.ListOptions{
		FieldSelector: "metadata.name=" + kubeconfigName,
	})
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), 1, len(backingConfigMap.Items))
}

func (kc *ExtKubeconfigTestSuite) TestCreateKubeconfigAsAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for cluster: %s", rbac.Admin.String(), kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigapi.KubeconfigFile, []string{kc.cluster.ID}, kc.client.RancherConfig.Host, false, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := settings.GetGlobalSettingDefaultValue(kc.client, settings.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	log.Infof("Validating kubeconfig and backing resources for kubeconfig: %s", createdKubeconfig.Name)
	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.cluster.ID}, userID, kc.cluster.ID, expectedTTL, kubeconfigapi.NonAceClusterType)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigTestSuite) TestCreateKubeconfigAsClusterOwner() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Create a user and add the user to the downstream cluster with role %s", rbac.ClusterOwner.String())
	createdUser, standardUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", createdUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(standardUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigapi.KubeconfigFile, []string{kc.cluster.ID}, kc.client.RancherConfig.Host, false, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := settings.GetGlobalSettingDefaultValue(kc.client, settings.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	log.Infof("Validating kubeconfig and backing resources for kubeconfig: %s", createdKubeconfig.Name)
	kc.validateKubeconfigAndBackingResources(kc.client, standardUserClient, createdKubeconfig.Name,
		[]string{kc.cluster.ID}, createdUser.ID, kc.cluster.ID, expectedTTL, kubeconfigapi.NonAceClusterType)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, true)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigTestSuite) TestCreateKubeconfigForAceCluster() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for the ACE cluster: %s", rbac.Admin.String(), kc.aceCluster1.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.aceCluster1.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigapi.KubeconfigFile, []string{kc.aceCluster1.ID}, kc.client.RancherConfig.Host, true, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := settings.GetGlobalSettingDefaultValue(kc.client, settings.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	log.Infof("Validating kubeconfig and backing resources for kubeconfig: %s", createdKubeconfig.Name)
	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.aceCluster1.ID}, userID, kc.aceCluster1.ID, expectedTTL, kubeconfigapi.AceClusterType)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigTestSuite) TestCreateKubeconfigMultipleClusters() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for clusters: %s and %s", rbac.Admin.String(), kc.cluster2.ID, kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster2.ID, kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigapi.KubeconfigFile, []string{kc.cluster2.ID, kc.cluster.ID}, kc.client.RancherConfig.Host, false, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := settings.GetGlobalSettingDefaultValue(kc.client, settings.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.cluster2.ID, kc.cluster.ID}, userID, kc.cluster2.ID, expectedTTL, kubeconfigapi.NonAceClusterType)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigTestSuite) TestCreateKubeconfigMultipleAceClusters() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for ACE enabled clusters: %s and %s", rbac.Admin.String(), kc.aceCluster1.ID, kc.aceCluster2.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.aceCluster1.ID, kc.aceCluster2.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContentMixed(kc.client, kubeconfigapi.KubeconfigFile, []string{}, []string{kc.aceCluster1.ID, kc.aceCluster2.ID}, kc.client.RancherConfig.Host, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := settings.GetGlobalSettingDefaultValue(kc.client, settings.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.aceCluster1.ID, kc.aceCluster2.ID}, userID, kc.aceCluster1.ID, expectedTTL, kubeconfigapi.AceClusterType)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigTestSuite) TestCreateKubeconfigForUnauthorizedUser() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating a standard user with no access to downstream cluster %s", kc.cluster.ID)
	createdUser, unauthorizedUserClient, err := rbac.SetupUser(kc.client, rbac.BaseUser.String())
	require.NoError(kc.T(), err, "Failed to create a user")
	require.NotNil(kc.T(), createdUser)

	log.Infof("As user %s (%s) attempt to create kubeconfig for cluster: %s", createdUser.Name, createdUser.ID, kc.cluster.ID)
	kubeconfigObj, err := kubeconfigapi.CreateKubeconfig(unauthorizedUserClient, []string{kc.cluster.ID}, "", nil)
	require.Error(kc.T(), err, "Expected kubeconfig creation to fail for unauthorized user")
	require.Nil(kc.T(), kubeconfigObj, "Kubeconfig object should not be created for unauthorized user")
	expectedErr := "failed to create kubeconfig: kubeconfigs.ext.cattle.io is forbidden: user " + createdUser.ID + " is not allowed to access cluster " + kc.cluster.ID
	require.Contains(kc.T(), err.Error(), expectedErr, "Error should mention forbidden access, got: %s", err.Error())
	require.True(kc.T(), k8serrors.IsForbidden(err))
}

func (kc *ExtKubeconfigTestSuite) TestGetKubeconfigAsAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	firstUser, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	secondUser, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As admin, creating a kubeconfig for cluster : %s", kc.cluster.ID)
	adminKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKubeconfig.Status.Value)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the admin")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(adminKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", firstUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	firstUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", secondUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	secondUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserKubeconfig.Status.Value)

	log.Infof("Verifying that the admin can access all kubeconfigs")
	kcObjAdmin, err := kubeconfigapi.GetKubeconfig(kc.client, adminKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), adminKubeconfig.Name, kcObjAdmin.Name)

	kcObjFirstUser, err := kubeconfigapi.GetKubeconfig(kc.client, firstUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), firstUserKubeconfig.Name, kcObjFirstUser.Name)

	kcObjSecondUser, err := kubeconfigapi.GetKubeconfig(kc.client, secondUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), secondUserKubeconfig.Name, kcObjSecondUser.Name)
}

func (kc *ExtKubeconfigTestSuite) TestGetKubeconfigAsNonAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	firstUser, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	secondUser, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As admin, creating a kubeconfig for cluster : %s", kc.cluster.ID)
	adminKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", firstUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	firstUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserKubeconfig.Status.Value)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user %s", firstUser.ID)
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(firstUserKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, true)
	require.NoError(kc.T(), err)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", secondUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	secondUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserKubeconfig.Status.Value)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user %s", secondUser.ID)
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(secondUserKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, true)
	require.NoError(kc.T(), err)

	log.Infof("Verifying that the users can access their respective kubeconfig")
	kcObj1, err := kubeconfigapi.GetKubeconfig(firstUserClient, firstUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), firstUserKubeconfig.Name, kcObj1.Name)

	kcObj2, err := kubeconfigapi.GetKubeconfig(secondUserClient, secondUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), secondUserKubeconfig.Name, kcObj2.Name)

	log.Infof("Verifying a non-admin users cannot access another user's kubeconfig")
	_, err = kubeconfigapi.GetKubeconfig(firstUserClient, secondUserKubeconfig.Name)
	require.Error(kc.T(), err, "Non-admin user should not be able to access another user's kubeconfig")
	require.True(kc.T(), k8serrors.IsNotFound(err))
}

func (kc *ExtKubeconfigTestSuite) TestListKubeconfigAsAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	firstUser, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	secondUser, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As admin, creating a kubeconfig for cluster : %s", kc.cluster.ID)
	adminKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", firstUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	firstUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", secondUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	secondUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserKubeconfig.Status.Value)

	log.Infof("Verifying that the admin can list all kubeconfigs")
	kcObjAdmin, err := kubeconfigapi.ListKubeconfigs(kc.client)
	require.NoError(kc.T(), err)
	names := []string{}
	for _, kc := range kcObjAdmin.Items {
		names = append(names, kc.Name)
	}
	require.Contains(kc.T(), names, adminKubeconfig.Name)
	require.Contains(kc.T(), names, firstUserKubeconfig.Name)
	require.Contains(kc.T(), names, secondUserKubeconfig.Name)
}

func (kc *ExtKubeconfigTestSuite) TestListKubeconfigAsNonAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	firstUser, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	secondUser, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As admin, creating a kubeconfig for cluster : %s", kc.cluster.ID)
	adminKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", firstUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	firstUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", secondUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	secondUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserKubeconfig.Status.Value)

	log.Infof("Verifying that the users can list their respective kubeconfig")
	kcObj1, err := kubeconfigapi.ListKubeconfigs(firstUserClient)
	require.NoError(kc.T(), err)
	names := []string{}
	for _, kc := range kcObj1.Items {
		names = append(names, kc.Name)
	}
	require.NotContains(kc.T(), names, adminKubeconfig.Name)
	require.Contains(kc.T(), names, firstUserKubeconfig.Name)
	require.NotContains(kc.T(), names, secondUserKubeconfig.Name)

	kcObj2, err := kubeconfigapi.ListKubeconfigs(secondUserClient)
	require.NoError(kc.T(), err)
	names = []string{}
	for _, kc := range kcObj2.Items {
		names = append(names, kc.Name)
	}
	require.NotContains(kc.T(), names, adminKubeconfig.Name)
	require.NotContains(kc.T(), names, firstUserKubeconfig.Name)
	require.Contains(kc.T(), names, secondUserKubeconfig.Name)
}

func (kc *ExtKubeconfigTestSuite) TestUpdateKubeconfigAsAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	firstUser, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	secondUser, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As admin, creating a kubeconfig for cluster : %s", kc.cluster.ID)
	adminKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", firstUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	firstUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", secondUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	secondUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserKubeconfig.Status.Value)

	log.Infof("As an admin, updating own kubeconfig")
	kcToUpdate := adminKubeconfig.DeepCopy()
	kcToUpdate.Spec.Description = "Updated by admin"
	if kcToUpdate.Labels == nil {
		kcToUpdate.Labels = map[string]string{}
	}
	kcToUpdate.Labels["edited-by"] = "admin"
	if kcToUpdate.Annotations == nil {
		kcToUpdate.Annotations = map[string]string{}
	}
	kcToUpdate.Annotations["note"] = "admin update"
	kcToUpdate.Finalizers = append(kcToUpdate.Finalizers, kubeconfigapi.DummyFinalizer)

	updatedKc, err := kubeconfigapi.UpdateKubeconfig(kc.client, kcToUpdate)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), "Updated by admin", updatedKc.Spec.Description)
	require.Equal(kc.T(), "admin", updatedKc.Labels["edited-by"])
	require.Equal(kc.T(), "admin update", updatedKc.Annotations["note"])
	require.Contains(kc.T(), updatedKc.Finalizers, kubeconfigapi.DummyFinalizer)

	log.Infof("As an admin, attempting to update immutable field spec.clusters")
	kcImmutable := kcToUpdate.DeepCopy()
	kcImmutable.Spec.Clusters = []string{"c-m-immutable"}
	_, err = kubeconfigapi.UpdateKubeconfig(kc.client, kcImmutable)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "spec.clusters is immutable")

	log.Infof("As an admin, updating the non-admin user's kubeconfig")
	kcToUpdate = secondUserKubeconfig.DeepCopy()
	kcToUpdate.Spec.Description = "Updated by admin"
	if kcToUpdate.Labels == nil {
		kcToUpdate.Labels = map[string]string{}
	}
	kcToUpdate.Labels["edited-by"] = "admin"
	if kcToUpdate.Annotations == nil {
		kcToUpdate.Annotations = map[string]string{}
	}
	kcToUpdate.Annotations["note"] = "admin update"
	kcToUpdate.Finalizers = append(kcToUpdate.Finalizers, kubeconfigapi.DummyFinalizer)

	updatedKc, err = kubeconfigapi.UpdateKubeconfig(kc.client, kcToUpdate)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), "Updated by admin", updatedKc.Spec.Description)
	require.Equal(kc.T(), "admin", updatedKc.Labels["edited-by"])
	require.Equal(kc.T(), "admin update", updatedKc.Annotations["note"])
	require.Contains(kc.T(), updatedKc.Finalizers, kubeconfigapi.DummyFinalizer)

	log.Infof("As an admin, attempting to update immutable field spec.clusters")
	kcImmutable = kcToUpdate.DeepCopy()
	kcImmutable.Spec.Clusters = []string{"c-m-immutable"}
	_, err = kubeconfigapi.UpdateKubeconfig(kc.client, kcImmutable)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "spec.clusters is immutable")
}

func (kc *ExtKubeconfigTestSuite) TestUpdateKubeconfigAsNonAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	firstUser, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	secondUser, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("As admin, creating a kubeconfig for cluster : %s", kc.cluster.ID)
	adminKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", firstUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	firstUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserKubeconfig.Status.Value)

	log.Infof("As user %s with role %s, creating a kubeconfig for cluster: %s", secondUser.Name, rbac.ClusterOwner.String(), kc.cluster.ID)
	secondUserKubeconfig, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserKubeconfig.Status.Value)

	log.Infof("As user %s, updating own kubeconfig", firstUser.Name)
	kcToUpdate := firstUserKubeconfig.DeepCopy()
	kcToUpdate.Spec.Description = "Updated by non-admin user"
	if kcToUpdate.Labels == nil {
		kcToUpdate.Labels = map[string]string{}
	}
	kcToUpdate.Labels["edited-by"] = firstUser.Name
	if kcToUpdate.Annotations == nil {
		kcToUpdate.Annotations = map[string]string{}
	}
	kcToUpdate.Annotations["note"] = "user update"
	kcToUpdate.Finalizers = append(kcToUpdate.Finalizers, kubeconfigapi.DummyFinalizer)

	updatedKc, err := kubeconfigapi.UpdateKubeconfig(firstUserClient, kcToUpdate)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), "Updated by non-admin user", updatedKc.Spec.Description)
	require.Equal(kc.T(), firstUser.Name, updatedKc.Labels["edited-by"])
	require.Equal(kc.T(), "user update", updatedKc.Annotations["note"])
	require.Contains(kc.T(), updatedKc.Finalizers, kubeconfigapi.DummyFinalizer)

	log.Infof("As user %s, attempting to update immutable field spec.clusters", firstUser.Name)
	kcImmutable := kcToUpdate.DeepCopy()
	kcImmutable.Spec.Clusters = []string{"c-m-immutable"}
	_, err = kubeconfigapi.UpdateKubeconfig(firstUserClient, kcImmutable)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "spec.clusters is immutable")

	log.Infof("As user %s, attempting to update kubeconfig owned by user %s", firstUser.Name, secondUser.Name)
	kcToUpdate = secondUserKubeconfig.DeepCopy()
	kcToUpdate.Spec.Description = "Forbidden update by non-admin user"
	kcToUpdate.Labels = map[string]string{"edited-by": firstUser.Name}

	_, err = kubeconfigapi.UpdateKubeconfig(firstUserClient, kcToUpdate)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsNotFound(err))
}

func (kc *ExtKubeconfigTestSuite) TestDeleteKubeconfigAsAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two base users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	_, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	_, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("Creating kubeconfigs for admin and the non-admin users")
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	firstUserKc, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	secondUserKc, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)

	log.Infof("As admin, deleting all kubeconfigs")
	err = kubeconfigapi.DeleteKubeconfig(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.DeleteKubeconfig(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.DeleteKubeconfig(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err)

	log.Infof("Verifying backing resources are deleted when kubeconfig is deleted")
	err = kubeconfigapi.WaitForBackingTokenDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing token %s should be deleted when kubeconfig is deleted", adminKc.Name)
	err = kubeconfigapi.WaitForBackingTokenDeletion(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing token %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingTokenDeletion(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing token %s should be deleted when kubeconfig is deleted", secondUserKc.Name)

	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", adminKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", secondUserKc.Name)
}

func (kc *ExtKubeconfigTestSuite) TestDeleteKubeconfigAsNonAdmin() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("Creating two users and adding the users to the downstream cluster with role %s", rbac.ClusterOwner.String())
	_, firstUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")
	_, secondUserClient, err := rbac.AddUserWithRoleToCluster(kc.client, rbac.BaseUser.String(), rbac.ClusterOwner.String(), kc.cluster, nil)
	require.NoError(kc.T(), err, "Failed to create standard user with cluster owner role")

	log.Infof("Creating kubeconfigs for admin and both users")
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	firstUserKc, err := kubeconfigapi.CreateKubeconfig(firstUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	secondUserKc, err := kubeconfigapi.CreateKubeconfig(secondUserClient, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)

	log.Infof("As non-admin users, attempting to delete each other's and admin's kubeconfigs")
	err = kubeconfigapi.DeleteKubeconfig(firstUserClient, adminKc.Name)
	require.Error(kc.T(), err, "Non-admin user should not be able to delete admin's kubeconfig")
	require.True(kc.T(), k8serrors.IsNotFound(err))
	err = kubeconfigapi.DeleteKubeconfig(secondUserClient, firstUserKc.Name)
	require.Error(kc.T(), err, "Non-admin user should not be able to delete another user's kubeconfig")
	require.True(kc.T(), k8serrors.IsNotFound(err))

	log.Infof("As non-admin users, verifying kubeconfig owned by self can be deleted")
	err = kubeconfigapi.DeleteKubeconfig(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.DeleteKubeconfig(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err)

	log.Infof("Verifying backing resources are deleted when kubeconfig is deleted")
	err = kubeconfigapi.WaitForBackingTokenDeletion(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing token %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingTokenDeletion(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing token %s should be deleted when kubeconfig is deleted", secondUserKc.Name)

	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", secondUserKc.Name)
}

func (kc *ExtKubeconfigTestSuite) TestKubeconfigAfterBackingTokensDeleted() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a kubeconfig for cluster: %s", kc.cluster.ID)
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKc.Status.Value)

	log.Infof("Validating backing tokens are created for kubeconfig %q", adminKc.Name)
	tokens, err := kubeconfigapi.GetBackingTokensForKubeconfigName(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens, "Expected at least one backing token for kubeconfig")

	log.Infof("Deleting backing token created for kubeconfig %q", adminKc.Name)
	err = kc.client.Management.Token.Delete(&tokens[0])
	require.NoError(kc.T(), err)

	log.Infof("Verifying that the kubeconfig %q is deleted automatically after backing token is deleted", adminKc.Name)
	err = kubeconfigapi.WaitForKubeconfigDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "timed out waiting for kubeconfig %s to be deleted", adminKc.Name)
}

func (kc *ExtKubeconfigTestSuite) TestKubeconfigCreationWithTTL() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	ttlSeconds := int64(600)
	log.Infof("As admin, creating a kubeconfig for cluster: %s", kc.cluster.ID)
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", &ttlSeconds)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKc.Status.Value)

	log.Infof("Validating backing tokens are created for kubeconfig %q", adminKc.Name)
	tokens, err := kubeconfigapi.GetBackingTokensForKubeconfigName(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens, "Expected at least one backing token for kubeconfig")

	log.Infof("Validating backing config map is created for kubeconfig %q", adminKc.Name)
	backingConfigMap, err := kc.client.WranglerContext.Core.ConfigMap().List(kubeconfigapi.KubeconfigConfigmapNamespace, metav1.ListOptions{
		FieldSelector: "metadata.name=" + adminKc.Name,
	})
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), 1, len(backingConfigMap.Items))

	log.Infof("Validating TTL of kubeconfig %q, backing token %q and backing config map %q matches the requested TTL", adminKc.Name, tokens[0].Name, backingConfigMap.Items[0].Name)
	require.Equal(kc.T(), ttlSeconds, adminKc.Spec.TTL, "Kubeconfig spec.ttl should match the TTL")
	require.Equal(kc.T(), ttlSeconds*1000, tokens[0].TTLMillis, "Backing token TTL should match requested TTL")
	require.Equal(kc.T(), strconv.FormatInt(ttlSeconds, 10), backingConfigMap.Items[0].Data["ttl"], "Backing ConfigMap TTL should match requested TTL")
}

func (kc *ExtKubeconfigTestSuite) TestKubeconfigWithCurrentContext() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a kubeconfig for cluster: %s", kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID, kc.cluster2.ID}, kc.cluster2.ID, nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigapi.KubeconfigFile, []string{kc.cluster.ID, kc.cluster2.ID}, kc.client.RancherConfig.Host, false, kc.cluster2.Name)
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := settings.GetGlobalSettingDefaultValue(kc.client, settings.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{kc.cluster.ID, kc.cluster2.ID}, userID, kc.cluster2.ID, expectedTTL, kubeconfigapi.NonAceClusterType)

	log.Infof("Verifying the current context is set to cluster %s", kc.cluster2.Name)
	kcCurrContext, err := kubeconfigapi.GetCurrentContext(kubeconfigapi.KubeconfigFile)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kc.cluster2.Name, kcCurrContext, "current-context mismatch")

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)
}

func TestExtKubeconfigTestSuite(t *testing.T) {
	suite.Run(t, new(ExtKubeconfigTestSuite))
}

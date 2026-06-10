//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package kubeconfigs

import (
	"os"
	"strconv"
	"strings"
	"testing"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"

	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extconfigmapapi "github.com/rancher/shepherd/extensions/kubeapi/configmaps"
	extkubeconfigapi "github.com/rancher/shepherd/extensions/kubeapi/kubeconfigs"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/extensions/users"

	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	configDefaults "github.com/rancher/tests/actions/config/defaults"
	kubeconfigapi "github.com/rancher/tests/actions/kubeapi/kubeconfigs"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ExtKubeconfigExtTokenTestSuite struct {
	suite.Suite
	client      *rancher.Client
	session     *session.Session
	cluster     *management.Cluster
	aceCluster1 *management.Cluster
	aceCluster2 *management.Cluster
	cluster2    *management.Cluster
}

func (kc *ExtKubeconfigExtTokenTestSuite) SetupSuite() {
	kc.session = session.NewSession()

	client, err := rancher.NewClient("", kc.session)
	require.NoError(kc.T(), err)
	kc.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(kc.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := extclusters.GetClusterIDByName(kc.client, clusterName)
	require.NoError(kc.T(), err, "Error getting cluster ID")
	kc.cluster, err = kc.client.Management.Cluster.ByID(clusterID)
	require.NoError(kc.T(), err)

	log.Infof("Creating additional clusters for kubeconfig tests")
	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	cattleConfig, err = configDefaults.SetK8sDefault(client, configDefaults.K3S, cattleConfig)
	require.NoError(kc.T(), err)

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(configDefaults.ClusterConfigKey, cattleConfig, clusterConfig)
	clusterConfig.MachinePools = nodeRolesAll
	provider := provisioning.CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

	log.Infof("Creating ACE-disabled clusters")
	clusterObject2, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), clusterObject2)
	cluster2ID, err := extclusters.GetClusterIDByName(kc.client, clusterObject2.Name)
	require.NoError(kc.T(), err)

	log.Infof("Creating ACE-enabled clusters")
	networking := provisioninginput.Networking{
		LocalClusterAuthEndpoint: &rkev1.LocalClusterAuthEndpoint{
			Enabled: true,
		},
	}
	clusterConfig.Networking = &networking
	aceClusterObject1, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), aceClusterObject1)
	aceCluster1ID, err := extclusters.GetClusterIDByName(kc.client, aceClusterObject1.Name)
	require.NoError(kc.T(), err)

	aceClusterObject2, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(kc.T(), err)
	require.NotNil(kc.T(), aceClusterObject2)
	aceCluster2ID, err := extclusters.GetClusterIDByName(kc.client, aceClusterObject2.Name)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TearDownSuite() {
	kc.session.Cleanup()
}

func (kc *ExtKubeconfigExtTokenTestSuite) validateKubeconfigAndBackingResources(client *rancher.Client, userClient *rancher.Client, kubeconfigName string, expectedClusters []string, expectedUserID string,
	expectedCurrentContext string, expectedTTL int64, clusterType string) {

	log.Infof("Validating kubeconfig object and its spec fields, status, and labels")
	kubeconfigObj, err := extkubeconfigapi.GetKubeconfigByName(client, kubeconfigName)
	require.NoError(kc.T(), err)

	log.Infof("Validating kubeconfig has the label cattle.io/user-id and it matches the expected user ID: %s", expectedUserID)
	userID, ok := kubeconfigObj.Labels[kubeconfigapi.UserIDLabel]
	require.True(kc.T(), ok, "Expected label cattle.io/user-id to exist on kubeconfig")
	require.Equal(kc.T(), expectedUserID, userID, "Label cattle.io/user-id should match the creator's user ID")

	log.Infof("Validating the kubeconfig spec fields: clusters, status, currentContext, and TTL")
	err = kubeconfigapi.VerifyKubeconfigSpec(kubeconfigObj, expectedClusters, expectedCurrentContext, expectedTTL, clusterType)
	require.NoError(kc.T(), err, "Kubeconfig spec validation failed")
	require.Equal(kc.T(), kubeconfigapi.StatusCompletedSummary, kubeconfigObj.Status.Summary)

	err = kubeconfigapi.VerifyKubeconfigExtTokens(client, kubeconfigObj, clusterType)
	require.NoError(kc.T(), err)

	log.Infof("Validating user tokens in the kubeconfig use the ext token format ext/<name>:<value>")
	err = kubeconfigapi.VerifyKubeconfigExtTokenPrefix(kubeconfigapi.KubeconfigFile)
	require.NoError(kc.T(), err)

	log.Infof("Validating backing ext tokens are created for kubeconfig %q", kubeconfigName)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(userClient, kubeconfigName)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens.Items, "Expected at least one backing token for kubeconfig")

	expectedTokenCount := 1
	if strings.ToLower(clusterType) == kubeconfigapi.AceClusterType {
		expectedTokenCount = len(expectedClusters) + 1
	}
	require.Equal(kc.T(), expectedTokenCount, len(tokens.Items),
		"Expected %d backing tokens for cluster type %s, got %d. Kubeconfig has: %s",
		expectedTokenCount, clusterType, len(tokens.Items), kubeconfigName)
	log.Infof("Number of backing ext tokens: %d", len(tokens.Items))

	log.Infof("Validating each backing ext token's TTL matches the expected TTL (in milliseconds)")
	expectedTokenTTLMillis := expectedTTL * 1000
	for _, token := range tokens.Items {
		require.Equal(kc.T(), expectedTokenTTLMillis, token.Spec.TTL,
			"Backing ext token %q TTL should be %d ms (kubeconfig-default-token-ttl-minutes * 60 * 1000)", token.Name, expectedTokenTTLMillis)
	}

	log.Infof("Validating no v3 tokens are created for kubeconfig %q, only ext tokens", kubeconfigName)
	v3Tokens, err := kubeconfigapi.GetBackingV3TokensForKubeconfig(userClient, kubeconfigName)
	require.NoError(kc.T(), err)
	require.Empty(kc.T(), v3Tokens, "Expected no v3 backing tokens for kubeconfig %q, only ext tokens should be created", kubeconfigName)

	log.Infof("Validating that backing configmap for kubeconfig %q and a secret for each backing ext token are created in the namespace %q", kubeconfigName, kubeconfigapi.KubeconfigConfigmapNamespace)
	err = kubeconfigapi.VerifyExtKubeconfigBackingResources(client, kubeconfigName)
	require.NoError(kc.T(), err)

	log.Infof("Validating labels on the kubeconfig and its backing resources (ext tokens, v1 Secrets, ConfigMap)")
	err = kubeconfigapi.VerifyKubeconfigResourceLabels(client, kubeconfigObj, expectedUserID)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigForLocalCluster() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for the local cluster: %s", rbac.Admin.String(), extclusterapi.LocalCluster)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	log.Infof("Validating the kubeconfig content")
	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	err = kubeconfigapi.VerifyKubeconfigContent(kc.client, kubeconfigapi.KubeconfigFile, []string{extclusterapi.LocalCluster}, kc.client.RancherConfig.Host, false, "")
	require.NoError(kc.T(), err, "Kubeconfig content validation failed")

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	ttlInt, err := strconv.Atoi(ttlStr)
	require.NoError(kc.T(), err)
	expectedTTL := int64(ttlInt * 60)

	log.Infof("Validating kubeconfig and backing resources for kubeconfig: %s", createdKubeconfig.Name)
	userID, err := users.GetUserIDByName(kc.client, rbac.Admin.String())
	require.NoError(kc.T(), err)
	kc.validateKubeconfigAndBackingResources(kc.client, kc.client, createdKubeconfig.Name,
		[]string{extclusterapi.LocalCluster}, userID, extclusterapi.LocalCluster, expectedTTL, kubeconfigapi.NonAceClusterType)

	log.Infof("Verifying all contexts in the kubeconfig are usable by the user")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigAsAdmin() {
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

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigAsClusterOwner() {
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

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigForAceCluster() {
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

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigMultipleClusters() {
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

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigMultipleAceClusters() {
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

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestCreateKubeconfigForUnauthorizedUser() {
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestGetKubeconfigAsAdmin() {
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
	kcObjAdmin, err := extkubeconfigapi.GetKubeconfigByName(kc.client, adminKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), adminKubeconfig.Name, kcObjAdmin.Name)

	kcObjFirstUser, err := extkubeconfigapi.GetKubeconfigByName(kc.client, firstUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), firstUserKubeconfig.Name, kcObjFirstUser.Name)

	kcObjSecondUser, err := extkubeconfigapi.GetKubeconfigByName(kc.client, secondUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), secondUserKubeconfig.Name, kcObjSecondUser.Name)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestGetKubeconfigAsNonAdmin() {
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
	kcObj1, err := extkubeconfigapi.GetKubeconfigByName(firstUserClient, firstUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), firstUserKubeconfig.Name, kcObj1.Name)

	kcObj2, err := extkubeconfigapi.GetKubeconfigByName(secondUserClient, secondUserKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), secondUserKubeconfig.Name, kcObj2.Name)

	log.Infof("Verifying a non-admin user cannot access another user's kubeconfig")
	_, err = extkubeconfigapi.GetKubeconfigByName(firstUserClient, secondUserKubeconfig.Name)
	require.Error(kc.T(), err, "Non-admin user should not be able to access another user's kubeconfig")
	require.True(kc.T(), k8serrors.IsNotFound(err))
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestListKubeconfigAsAdmin() {
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
	kcObjAdmin, err := extkubeconfigapi.ListKubeconfigs(kc.client, metav1.ListOptions{})
	require.NoError(kc.T(), err)
	names := []string{}
	for _, kc := range kcObjAdmin.Items {
		names = append(names, kc.Name)
	}
	require.Contains(kc.T(), names, adminKubeconfig.Name)
	require.Contains(kc.T(), names, firstUserKubeconfig.Name)
	require.Contains(kc.T(), names, secondUserKubeconfig.Name)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestListKubeconfigAsNonAdmin() {
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
	kcObj1, err := extkubeconfigapi.ListKubeconfigs(firstUserClient, metav1.ListOptions{})
	require.NoError(kc.T(), err)
	names := []string{}
	for _, kc := range kcObj1.Items {
		names = append(names, kc.Name)
	}
	require.NotContains(kc.T(), names, adminKubeconfig.Name)
	require.Contains(kc.T(), names, firstUserKubeconfig.Name)
	require.NotContains(kc.T(), names, secondUserKubeconfig.Name)

	kcObj2, err := extkubeconfigapi.ListKubeconfigs(secondUserClient, metav1.ListOptions{})
	require.NoError(kc.T(), err)
	names = []string{}
	for _, kc := range kcObj2.Items {
		names = append(names, kc.Name)
	}
	require.NotContains(kc.T(), names, adminKubeconfig.Name)
	require.NotContains(kc.T(), names, firstUserKubeconfig.Name)
	require.Contains(kc.T(), names, secondUserKubeconfig.Name)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestUpdateKubeconfigAsAdmin() {
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

	updatedKc, err := extkubeconfigapi.UpdateKubeconfig(kc.client, kcToUpdate)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), "Updated by admin", updatedKc.Spec.Description)
	require.Equal(kc.T(), "admin", updatedKc.Labels["edited-by"])
	require.Equal(kc.T(), "admin update", updatedKc.Annotations["note"])
	require.Contains(kc.T(), updatedKc.Finalizers, kubeconfigapi.DummyFinalizer)

	log.Infof("As an admin, attempting to update immutable field spec.clusters")
	kcImmutable := kcToUpdate.DeepCopy()
	kcImmutable.Spec.Clusters = []string{"c-m-immutable"}
	_, err = extkubeconfigapi.UpdateKubeconfig(kc.client, kcImmutable)
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

	updatedKc, err = extkubeconfigapi.UpdateKubeconfig(kc.client, kcToUpdate)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), "Updated by admin", updatedKc.Spec.Description)
	require.Equal(kc.T(), "admin", updatedKc.Labels["edited-by"])
	require.Equal(kc.T(), "admin update", updatedKc.Annotations["note"])
	require.Contains(kc.T(), updatedKc.Finalizers, kubeconfigapi.DummyFinalizer)

	log.Infof("As an admin, attempting to update immutable field spec.clusters")
	kcImmutable = kcToUpdate.DeepCopy()
	kcImmutable.Spec.Clusters = []string{"c-m-immutable"}
	_, err = extkubeconfigapi.UpdateKubeconfig(kc.client, kcImmutable)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "spec.clusters is immutable")
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestUpdateKubeconfigAsNonAdmin() {
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

	updatedKc, err := extkubeconfigapi.UpdateKubeconfig(firstUserClient, kcToUpdate)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), "Updated by non-admin user", updatedKc.Spec.Description)
	require.Equal(kc.T(), firstUser.Name, updatedKc.Labels["edited-by"])
	require.Equal(kc.T(), "user update", updatedKc.Annotations["note"])
	require.Contains(kc.T(), updatedKc.Finalizers, kubeconfigapi.DummyFinalizer)

	log.Infof("As user %s, attempting to update immutable field spec.clusters", firstUser.Name)
	kcImmutable := kcToUpdate.DeepCopy()
	kcImmutable.Spec.Clusters = []string{"c-m-immutable"}
	_, err = extkubeconfigapi.UpdateKubeconfig(firstUserClient, kcImmutable)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "spec.clusters is immutable")

	log.Infof("As user %s, attempting to update kubeconfig owned by user %s", firstUser.Name, secondUser.Name)
	kcToUpdate = secondUserKubeconfig.DeepCopy()
	kcToUpdate.Spec.Description = "Forbidden update by non-admin user"
	kcToUpdate.Labels = map[string]string{"edited-by": firstUser.Name}

	_, err = extkubeconfigapi.UpdateKubeconfig(firstUserClient, kcToUpdate)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsNotFound(err))
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestDeleteKubeconfigAsAdmin() {
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

	log.Infof("Waiting for the backing ext tokens to be created for each kubeconfig before deletion")
	err = kubeconfigapi.WaitForBackingExtTokenCreation(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", adminKc.Name)
	err = kubeconfigapi.WaitForBackingExtTokenCreation(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingExtTokenCreation(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", secondUserKc.Name)

	log.Infof("Capturing the backing ext tokens for each kubeconfig before deletion")
	adminTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminTokens.Items, "Expected at least one backing ext token for kubeconfig")
	firstUserTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserTokens.Items, "Expected at least one backing ext token for kubeconfig")
	secondUserTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserTokens.Items, "Expected at least one backing ext token for kubeconfig")

	log.Infof("Waiting for the backing ConfigMap and Secrets to be created for each kubeconfig before deletion")
	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", adminKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", secondUserKc.Name)
	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, adminTokens)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, firstUserTokens)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, secondUserTokens)
	require.NoError(kc.T(), err)

	log.Infof("As admin, deleting all kubeconfigs")
	err = extkubeconfigapi.DeleteKubeconfig(kc.client, adminKc.Name, true)
	require.NoError(kc.T(), err)
	err = extkubeconfigapi.DeleteKubeconfig(kc.client, firstUserKc.Name, true)
	require.NoError(kc.T(), err)
	err = extkubeconfigapi.DeleteKubeconfig(kc.client, secondUserKc.Name, true)
	require.NoError(kc.T(), err)

	log.Infof("Verifying backing resources are deleted when kubeconfig is deleted")
	err = kubeconfigapi.WaitForBackingExtTokenDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be deleted when kubeconfig is deleted", adminKc.Name)
	err = kubeconfigapi.WaitForBackingExtTokenDeletion(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingExtTokenDeletion(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be deleted when kubeconfig is deleted", secondUserKc.Name)

	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", adminKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", secondUserKc.Name)

	log.Infof("Verifying that the v1.Secret for each backing ext token is deleted")
	err = kubeconfigapi.WaitForBackingSecretDeletion(kc.client, adminTokens)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.WaitForBackingSecretDeletion(kc.client, firstUserTokens)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.WaitForBackingSecretDeletion(kc.client, secondUserTokens)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestDeleteKubeconfigAsNonAdmin() {
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

	err = kubeconfigapi.WaitForBackingExtTokenCreation(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingExtTokenCreation(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", secondUserKc.Name)

	firstUserTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), firstUserTokens.Items, "Expected at least one backing ext token for kubeconfig")
	secondUserTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), secondUserTokens.Items, "Expected at least one backing ext token for kubeconfig")

	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", secondUserKc.Name)

	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, firstUserTokens)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, secondUserTokens)
	require.NoError(kc.T(), err)

	log.Infof("As non-admin users, attempting to delete each other's and admin's kubeconfigs")
	err = extkubeconfigapi.DeleteKubeconfig(firstUserClient, adminKc.Name, false)
	require.Error(kc.T(), err, "Non-admin user should not be able to delete admin's kubeconfig")
	require.True(kc.T(), k8serrors.IsNotFound(err))
	err = extkubeconfigapi.DeleteKubeconfig(secondUserClient, firstUserKc.Name, false)
	require.Error(kc.T(), err, "Non-admin user should not be able to delete another user's kubeconfig")
	require.True(kc.T(), k8serrors.IsNotFound(err))

	log.Infof("As non-admin users, verifying kubeconfig owned by self can be deleted")
	err = extkubeconfigapi.DeleteKubeconfig(firstUserClient, firstUserKc.Name, true)
	require.NoError(kc.T(), err)
	err = extkubeconfigapi.DeleteKubeconfig(secondUserClient, secondUserKc.Name, true)
	require.NoError(kc.T(), err)

	log.Infof("Verifying backing resources are deleted when kubeconfig is deleted")
	err = kubeconfigapi.WaitForBackingExtTokenDeletion(firstUserClient, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingExtTokenDeletion(secondUserClient, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be deleted when kubeconfig is deleted", secondUserKc.Name)

	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, firstUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", firstUserKc.Name)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, secondUserKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", secondUserKc.Name)

	log.Infof("Verifying that the v1.Secret for each backing ext token is deleted")
	err = kubeconfigapi.WaitForBackingSecretDeletion(kc.client, firstUserTokens)
	require.NoError(kc.T(), err)
	err = kubeconfigapi.WaitForBackingSecretDeletion(kc.client, secondUserTokens)
	require.NoError(kc.T(), err)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestDeleteKubeconfigAce() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a kubeconfig for ACE enabled clusters: %s and %s", kc.aceCluster1.ID, kc.aceCluster2.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.aceCluster1.ID, kc.aceCluster2.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	kubeconfigName := createdKubeconfig.Name
	log.Infof("Waiting for the backing resources to be created for kubeconfig %q before deletion", kubeconfigName)
	err = kubeconfigapi.WaitForBackingExtTokenCreation(kc.client, kubeconfigName)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", kubeconfigName)

	log.Infof("Capturing the backing ext tokens for kubeconfig %q before deletion", kubeconfigName)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, kubeconfigName)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens.Items, "Expected at least one backing ext token for kubeconfig")

	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, kubeconfigName)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", kubeconfigName)
	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, tokens)
	require.NoError(kc.T(), err)

	log.Infof("Deleting the kubeconfig %q and verifying it is deleted", kubeconfigName)
	err = extkubeconfigapi.DeleteKubeconfig(kc.client, kubeconfigName, true)
	require.NoError(kc.T(), err, "Kubeconfig %s should be deleted successfully", kubeconfigName)

	log.Infof("Verifying that all backing ext tokens for kubeconfig %q are deleted", kubeconfigName)
	err = kubeconfigapi.WaitForBackingExtTokenDeletion(kc.client, kubeconfigName)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be deleted when kubeconfig is deleted", kubeconfigName)

	log.Infof("Verifying that the v1.Secret for each backing ext token is deleted")
	err = kubeconfigapi.WaitForBackingSecretDeletion(kc.client, tokens)
	require.NoError(kc.T(), err)

	log.Infof("Verifying that the backing ConfigMap for kubeconfig %q is deleted", kubeconfigName)
	err = kubeconfigapi.WaitForBackingConfigMapDeletion(kc.client, kubeconfigName)
	require.NoError(kc.T(), err, "Backing configmap %s should be deleted when kubeconfig is deleted", kubeconfigName)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestDeleteBackingExtTokensNonAce() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a kubeconfig for cluster: %s", kc.cluster.ID)
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKc.Status.Value)

	log.Infof("Validating backing tokens are created for kubeconfig %q", adminKc.Name)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens.Items, "Expected at least one backing token for kubeconfig")

	log.Infof("Deleting backing token created for kubeconfig %q", adminKc.Name)
	err = exttokenapi.DeleteExtToken(kc.client, tokens.Items[0].Name, true)
	require.NoError(kc.T(), err)

	log.Infof("Verifying that the kubeconfig %q is deleted automatically after backing token is deleted", adminKc.Name)
	err = extkubeconfigapi.WaitForKubeconfigDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "timed out waiting for kubeconfig %s to be deleted", adminKc.Name)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestDeleteBackingExtTokensAce() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As admin, creating a kubeconfig for ACE enabled clusters: %s and %s", kc.aceCluster1.ID, kc.aceCluster2.ID)
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.aceCluster1.ID, kc.aceCluster2.ID}, "", nil)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKc.Status.Value)

	log.Infof("Validating multiple backing tokens are created for kubeconfig %q", adminKc.Name)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	require.Greater(kc.T(), len(tokens.Items), 1, "Expected more than one backing token for an ACE kubeconfig")

	log.Infof("Deleting only one of the backing ext tokens for kubeconfig %q", adminKc.Name)
	err = exttokenapi.DeleteExtToken(kc.client, tokens.Items[0].Name, true)
	require.NoError(kc.T(), err)

	log.Infof("Verifying that the kubeconfig %q still exists after deleting one backing token", adminKc.Name)
	_, err = extkubeconfigapi.GetKubeconfigByName(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Kubeconfig %s should still exist after deleting only one of its backing tokens", adminKc.Name)

	log.Infof("Deleting the remaining backing ext tokens for kubeconfig %q", adminKc.Name)
	for _, token := range tokens.Items[1:] {
		err = exttokenapi.DeleteExtToken(kc.client, token.Name, true)
		require.NoError(kc.T(), err)
	}

	log.Infof("Verifying that the kubeconfig %q is deleted automatically after all backing tokens are deleted", adminKc.Name)
	err = extkubeconfigapi.WaitForKubeconfigDeletion(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "timed out waiting for kubeconfig %s to be deleted", adminKc.Name)
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestKubeconfigWithCurrentContext() {
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

	ttlStr, err := extsettingsapi.GetGlobalSettingDefaultValue(kc.client, extsettingsapi.KubeconfigDefaultTTLMinutes)
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

func (kc *ExtKubeconfigExtTokenTestSuite) TestKubeconfigCreationWithTTL() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	ttlSeconds := int64(600)
	log.Infof("As admin, creating a kubeconfig for cluster: %s", kc.cluster.ID)
	adminKc, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", &ttlSeconds)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), adminKc.Status.Value)

	err = kubeconfigapi.WaitForBackingExtTokenCreation(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", adminKc.Name)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, adminKc.Name)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens.Items, "Expected at least one backing token for kubeconfig")

	err = kubeconfigapi.WaitForBackingConfigMapCreation(kc.client, adminKc.Name)
	require.NoError(kc.T(), err, "Backing configmap %s should be created", adminKc.Name)
	err = kubeconfigapi.WaitForBackingSecretCreation(kc.client, tokens)
	require.NoError(kc.T(), err)

	backingConfigMap, err := extconfigmapapi.GetConfigMapByName(kc.client, "local", kubeconfigapi.KubeconfigConfigmapNamespace, adminKc.Name)
	require.NoError(kc.T(), err)

	log.Infof("Validating TTL of kubeconfig %q, backing token %q and backing config map %q matches the requested TTL", adminKc.Name, tokens.Items[0].Name, backingConfigMap.Name)
	require.Equal(kc.T(), ttlSeconds, adminKc.Spec.TTL, "Kubeconfig spec.ttl should match the TTL")
	require.Equal(kc.T(), ttlSeconds*1000, tokens.Items[0].Spec.TTL, "Backing token TTL should match requested TTL")
	require.Equal(kc.T(), strconv.FormatInt(ttlSeconds, 10), backingConfigMap.Data["ttl"], "Backing ConfigMap TTL should match requested TTL")
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestKubeconfigTTLExpiryNonAce() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	ttlSeconds := int64(120)
	log.Infof("As admin, creating a kubeconfig with TTL %d seconds for ACE disabled cluster: %s", ttlSeconds, kc.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.cluster.ID}, "", &ttlSeconds)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	kubeconfigName := createdKubeconfig.Name
	require.Equal(kc.T(), ttlSeconds, createdKubeconfig.Spec.TTL, "Kubeconfig spec.ttl should match the requested TTL")

	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	log.Infof("Waiting for the backing ext tokens to be created and capturing them before expiry")
	err = kubeconfigapi.WaitForBackingExtTokenCreation(kc.client, kubeconfigName)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", kubeconfigName)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, kubeconfigName)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens.Items, "Expected at least one backing ext token for kubeconfig")

	log.Infof("Verifying that each context (Rancher and Cluster) can access the cluster nodes before the TTL expires")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err, "All contexts should be usable before the TTL expires")

	log.Infof("Waiting for the kubeconfig TTL of %d seconds to expire and verifying that each context (Rancher and Cluster) fails with an Unauthorized error", ttlSeconds)
	err = kubeconfigapi.VerifyAllContextsExpired(kubeconfigapi.KubeconfigFile)
	require.NoError(kc.T(), err, "All contexts should fail with an Unauthorized error after the TTL expires")
}

func (kc *ExtKubeconfigExtTokenTestSuite) TestKubeconfigTTLExpiryAce() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	ttlSeconds := int64(120)
	log.Infof("As admin, creating a kubeconfig with TTL %d seconds for ACE enabled clusters: %s and %s", ttlSeconds, kc.aceCluster1.ID, kc.aceCluster2.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{kc.aceCluster1.ID, kc.aceCluster2.ID}, "", &ttlSeconds)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), createdKubeconfig.Status.Value)

	kubeconfigName := createdKubeconfig.Name
	require.Equal(kc.T(), ttlSeconds, createdKubeconfig.Spec.TTL, "Kubeconfig spec.ttl should match the requested TTL")

	err = os.WriteFile(kubeconfigapi.KubeconfigFile, []byte(createdKubeconfig.Status.Value), 0644)
	require.NoError(kc.T(), err)
	defer os.Remove(kubeconfigapi.KubeconfigFile)

	log.Infof("Waiting for the backing ext tokens to be created and capturing them before expiry")
	err = kubeconfigapi.WaitForBackingExtTokenCreation(kc.client, kubeconfigName)
	require.NoError(kc.T(), err, "Backing ext tokens for kubeconfig %s should be created", kubeconfigName)
	tokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(kc.client, kubeconfigName)
	require.NoError(kc.T(), err)
	require.NotEmpty(kc.T(), tokens.Items, "Expected at least one backing ext token for kubeconfig")

	log.Infof("Verifying that each context (Rancher, Node and Cluster) can access the cluster nodes before the TTL expires")
	err = kubeconfigapi.VerifyAllContextsUsable(kubeconfigapi.KubeconfigFile, false)
	require.NoError(kc.T(), err, "All contexts should be usable before the TTL expires")

	log.Infof("Waiting for the kubeconfig TTL of %d seconds to expire and verifying that each context (Rancher, Node and Cluster) fails with an Unauthorized error", ttlSeconds)
	err = kubeconfigapi.VerifyAllContextsExpired(kubeconfigapi.KubeconfigFile)
	require.NoError(kc.T(), err, "All contexts should fail with an Unauthorized error after the TTL expires")
}

func TestExtKubeconfigExtTokenTestSuite(t *testing.T) {
	suite.Run(t, new(ExtKubeconfigExtTokenTestSuite))
}

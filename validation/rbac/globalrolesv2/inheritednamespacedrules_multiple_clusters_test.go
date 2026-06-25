//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package globalrolesv2

import (
	"fmt"
	"os"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	extpodapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/pods"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	configDefaults "github.com/rancher/tests/actions/config/defaults"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	statefulsetapi "github.com/rancher/tests/actions/kubeapi/workloads/statefulsets"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InheritedNamespacedRulesMultipleClustersTestSuite struct {
	suite.Suite
	client   *rancher.Client
	session  *session.Session
	cluster  *management.Cluster
	cluster2 *management.Cluster
	cluster3 *management.Cluster
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TearDownSuite() {
	inr.session.Cleanup()
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) SetupSuite() {
	inr.session = session.NewSession()

	client, err := rancher.NewClient("", inr.session)
	require.NoError(inr.T(), err)
	inr.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(inr.T(), clusterName, "Cluster name should be set in the config file")
	clusterID, err := extclusters.GetClusterIDByName(inr.client, clusterName)
	require.NoError(inr.T(), err, "Error getting cluster ID")
	inr.cluster, err = inr.client.Management.Cluster.ByID(clusterID)
	require.NoError(inr.T(), err)

	log.Infof("Creating additional clusters for tests")
	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	cattleConfig, err = configDefaults.SetK8sDefault(client, configDefaults.K3S, cattleConfig)
	require.NoError(inr.T(), err)

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(configDefaults.ClusterConfigKey, cattleConfig, clusterConfig)
	clusterConfig.MachinePools = nodeRolesAll
	provider := provisioning.CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

	clusterObject2, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(inr.T(), err)
	require.NotNil(inr.T(), clusterObject2)
	cluster2ID, err := extclusters.GetClusterIDByName(inr.client, clusterObject2.Name)
	require.NoError(inr.T(), err)

	clusterObject3, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
	require.NoError(inr.T(), err)
	require.NotNil(inr.T(), clusterObject3)
	cluster3ID, err := extclusters.GetClusterIDByName(inr.client, clusterObject3.Name)
	require.NoError(inr.T(), err)

	err = provisioning.VerifyClusterReady(client, clusterObject2)
	require.NoError(inr.T(), err)
	err = provisioning.VerifyClusterReady(client, clusterObject3)
	require.NoError(inr.T(), err)

	err = deployment.VerifyClusterDeployments(client, clusterObject2)
	require.NoError(inr.T(), err)
	err = deployment.VerifyClusterDeployments(client, clusterObject3)
	require.NoError(inr.T(), err)

	err = pods.VerifyClusterPods(client, clusterObject2)
	require.NoError(inr.T(), err)
	err = pods.VerifyClusterPods(client, clusterObject3)
	require.NoError(inr.T(), err)

	provisioning.VerifyDynamicCluster(inr.T(), client, clusterObject2)
	provisioning.VerifyDynamicCluster(inr.T(), client, clusterObject3)

	inr.cluster2, err = inr.client.Management.Cluster.ByID(cluster2ID)
	require.NoError(inr.T(), err)
	inr.cluster3, err = inr.client.Management.Cluster.ByID(cluster3ID)
	require.NoError(inr.T(), err)
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) createTestResources(client *rancher.Client, clusters []*management.Cluster) (map[string]*corev1.Namespace, *corev1.Namespace, *v3.User, string, map[string]*appsv1.Deployment, map[string]*corev1.Pod, *appsv1.StatefulSet, error) {
	log.Info("Creating the required resources for the test.")
	sharedNamespaceName := namegen.AppendRandomString("testns-")
	sharedNamespaces := make(map[string]*corev1.Namespace)
	deployments := make(map[string]*appsv1.Deployment)
	pods := make(map[string]*corev1.Pod)

	for _, cluster := range clusters {
		createdProject, err := projectapi.CreateProject(client, cluster.ID)
		require.NoError(inr.T(), err, "Failed to create project in cluster %s", cluster.ID)

		namespace, err := namespaceapi.CreateNamespace(client, cluster.ID, createdProject.Name, sharedNamespaceName, "", nil, nil)
		require.NoError(inr.T(), err, "Failed to create namespace %s in cluster %s", sharedNamespaceName, cluster.ID)
		sharedNamespaces[cluster.ID] = namespace

		deployment, err := deploymentapi.CreateDeployment(client, cluster.ID, namespace.Name, "", 1, "", "", false, false, false, true)
		require.NoError(inr.T(), err, "Failed to create deployment in namespace %s in cluster %s", namespace.Name, cluster.ID)
		deployments[cluster.ID] = deployment

		podList, err := extpodapi.ListPods(client, cluster.ID, namespace.Name, metav1.ListOptions{})
		require.NoError(inr.T(), err, "Failed to list pods in namespace %s in cluster %s", namespace.Name, cluster.ID)
		require.NotEmpty(inr.T(), podList.Items, "No pods found in namespace %s in cluster %s", namespace.Name, cluster.ID)
		pods[cluster.ID] = &podList.Items[0]
	}

	uniqueProject, err := projectapi.CreateProject(client, clusters[0].ID)
	require.NoError(inr.T(), err)
	uniqueNamespace, err := namespaceapi.CreateNamespace(client, clusters[0].ID, uniqueProject.Name, namegen.AppendRandomString("testns-"), "", nil, nil)
	require.NoError(inr.T(), err, "Failed to create unique namespace in cluster %s", clusters[0].ID)

	podTemplate := podapi.CreateContainerAndPodTemplate("")
	statefulset, err := statefulsetapi.CreateStatefulSet(client, extclusterapi.LocalCluster, rbac.DefaultNamespace, podTemplate, 1, true, "")
	require.NoError(inr.T(), err, "Failed to create statefulset in local cluster")

	createdUser, userPassword, err := userapi.CreateUserWithRoles(client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user")

	return sharedNamespaces, uniqueNamespace, createdUser, userPassword, deployments, pods, statefulset, nil
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TestInheritedNamespacedRulesAllClusters() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	clusters := []*management.Cluster{inr.cluster, inr.cluster2, inr.cluster3}
	sharedNamespaces, uniqueNamespace, createdUser, userPassword, deployments, pods, statefulset, err := inr.createTestResources(inr.client, clusters)
	require.NoError(inr.T(), err, "Failed to create test resources")

	sharedNamespaceName := sharedNamespaces[inr.cluster.ID].Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for the shared namespace %s that exists in all downstream clusters.", sharedNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		sharedNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err, "Failed to create GlobalRole")
	log.Infof("GlobalRole %s created successfully.", createdGlobalRole.Name)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the shared namespace of each downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, sharedNamespaceName)
	for _, cluster := range clusters {
		err = rbacapi.WaitForRoleExistence(inr.client, cluster.ID, sharedNamespaceName, expectedRoleName, true)
		require.NoError(inr.T(), err, "Failed to get role %s in namespace %s on cluster %s", expectedRoleName, sharedNamespaceName, cluster.Name)
	}

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	grbList, err := inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	require.NotNil(inr.T(), grbList, "GlobalRoleBinding should exist")

	log.Info("Verify that a RoleBinding is created in the shared namespace of each downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, sharedNamespaceName)
	for _, cluster := range clusters {
		rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, cluster.ID, sharedNamespaceName, expectedRoleBindingName)
		require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s on cluster %s", expectedRoleBindingName, sharedNamespaceName, cluster.Name)
		require.NotNil(inr.T(), rbs, "RoleBinding %s should exist in namespace %s on cluster %s", expectedRoleBindingName, sharedNamespaceName, cluster.Name)
	}

	log.Infof("Verifying user permissions for user %s are correct across all downstream clusters.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	for _, cluster := range clusters {
		pod := pods[cluster.ID]
		deployment := deployments[cluster.ID]

		log.Infof("Verifying permissions in cluster %s", cluster.ID)
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "get", "pods", sharedNamespaceName, pod.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", sharedNamespaceName, "", true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "get", "deployments", sharedNamespaceName, deployment.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", sharedNamespaceName, "", true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "create", "pods", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "delete", "pods", sharedNamespaceName, pod.Name, false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "create", "deployments", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "delete", "deployments", sharedNamespaceName, deployment.Name, false, false))
	}

	log.Infof("Verifying user has NO permissions in unique namespace %s", uniqueNamespace.Name)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", uniqueNamespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", uniqueNamespace.Name, "", false, false))

	log.Info("Verifying user CAN list statefulsets in local cluster.")
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Info("Verifying user CANNOT list statefulsets in downstream clusters.")
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", sharedNamespaceName, "", false, false))
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TestInheritedNamespacedRulesSomeClusters() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	allClusters := []*management.Cluster{inr.cluster, inr.cluster2, inr.cluster3}
	clustersWithoutNamespace := []*management.Cluster{inr.cluster2, inr.cluster3}
	sharedNamespaces, uniqueNamespace, createdUser, userPassword, _, _, statefulset, err := inr.createTestResources(inr.client, allClusters)
	require.NoError(inr.T(), err, "Failed to create test resources")

	sharedNamespaceName := sharedNamespaces[inr.cluster.ID].Name
	deployment, err := deploymentapi.CreateDeployment(inr.client, inr.cluster.ID, uniqueNamespace.Name, "", 1, "", "", false, false, false, true)
	require.NoError(inr.T(), err)

	podList, err := extpodapi.ListPods(inr.client, inr.cluster.ID, uniqueNamespace.Name, metav1.ListOptions{})
	require.NoError(inr.T(), err)
	require.NotEmpty(inr.T(), podList.Items, "No pods found in uniqueNamespace")
	pod := &podList.Items[0]

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s that exists only in cluster1.", uniqueNamespace.Name)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		uniqueNamespace.Name: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)
	log.Infof("GlobalRole %s created successfully.", createdGlobalRole.Name)

	log.Info("Verify that a Role is created in the uniqueNamespace of cluster1 where it exists.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, uniqueNamespace.Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, uniqueNamespace.Name, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s on cluster %s", expectedRoleName, uniqueNamespace.Name, inr.cluster.Name)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)

	log.Info("Verify that a RoleBinding is created in the uniqueNamespace of cluster1.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, uniqueNamespace.Name)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, uniqueNamespace.Name, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s on cluster %s", expectedRoleBindingName, uniqueNamespace.Name, inr.cluster.Name)
	require.NotNil(inr.T(), rbs, "RoleBinding %s should exist in namespace %s on cluster %s", expectedRoleBindingName, uniqueNamespace.Name, inr.cluster.Name)

	log.Infof("Verifying user permissions for user %s.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)

	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", uniqueNamespace.Name, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", uniqueNamespace.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", uniqueNamespace.Name, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", uniqueNamespace.Name, "", true, false))

	for _, cluster := range clustersWithoutNamespace {
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", sharedNamespaceName, "", false, false))
	}

	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", uniqueNamespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", sharedNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TestInheritedNamespacedRulesNonExistentNamespace() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	allClusters := []*management.Cluster{inr.cluster, inr.cluster2, inr.cluster3}
	_, _, createdUser, userPassword, _, _, statefulset, err := inr.createTestResources(inr.client, allClusters)
	require.NoError(inr.T(), err, "Failed to create test resources")

	nonExistentNamespaceName := namegen.AppendRandomString("testns-")

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for non-existent namespace %s.", nonExistentNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		nonExistentNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err, "Failed to create GlobalRole")
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRole should exist")

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verifying user permissions for user %s.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	for _, cluster := range allClusters {
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))
	}
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TestUpdateGlobalRoleToAddInheritedNamespacedRulesAllClusters() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	clusters := []*management.Cluster{inr.cluster, inr.cluster2, inr.cluster3}
	sharedNamespaces, _, createdUser, userPassword, deployments, pods, statefulset, err := inr.createTestResources(inr.client, clusters)
	require.NoError(inr.T(), err, "Failed to create test resources")

	sharedNamespaceName := sharedNamespaces[inr.cluster.ID].Name

	log.Info("Create a GlobalRole with regular rules only (no inheritedNamespacedRules).")
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, nil)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verifying initial user permissions for user %s (before adding inheritedNamespacedRules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
	for _, cluster := range clusters {
		pod := pods[cluster.ID]
		deployment := deployments[cluster.ID]
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "get", "pods", sharedNamespaceName, pod.Name, false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "get", "deployments", sharedNamespaceName, deployment.Name, false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", sharedNamespaceName, "", false, false))
	}

	log.Infof("Update the GlobalRole to include inheritedNamespacedRules for namespace %s.", sharedNamespaceName)
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		sharedNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = inheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the shared namespace of each downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, sharedNamespaceName)
	for _, cluster := range clusters {
		err = rbacapi.WaitForRoleExistence(inr.client, cluster.ID, sharedNamespaceName, expectedRoleName, true)
		require.NoError(inr.T(), err, "Failed to get role %s in namespace %s on cluster %s", expectedRoleName, sharedNamespaceName, cluster.Name)
	}

	log.Info("Verify that a RoleBinding is created in the shared namespace of each downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, sharedNamespaceName)
	for _, cluster := range clusters {
		rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, cluster.ID, sharedNamespaceName, expectedRoleBindingName)
		require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s on cluster %s", expectedRoleBindingName, sharedNamespaceName, cluster.Name)
		require.NotNil(inr.T(), rbs, "RoleBinding %s should exist in namespace %s on cluster %s", expectedRoleBindingName, sharedNamespaceName, cluster.Name)
	}

	log.Infof("Verifying updated user permissions for user %s (after adding inheritedNamespacedRules).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	for _, cluster := range clusters {
		pod := pods[cluster.ID]
		deployment := deployments[cluster.ID]
		log.Infof("Verifying permissions in cluster %s", cluster.ID)
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "get", "pods", sharedNamespaceName, pod.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", sharedNamespaceName, "", true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "get", "deployments", sharedNamespaceName, deployment.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", sharedNamespaceName, "", true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "create", "pods", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "delete", "pods", sharedNamespaceName, pod.Name, false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "create", "deployments", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "delete", "deployments", sharedNamespaceName, deployment.Name, false, false))
	}
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", sharedNamespaceName, "", false, false))
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TestUpdateGlobalRoleToAddInheritedNamespacedRulesSomeClusters() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	allClusters := []*management.Cluster{inr.cluster, inr.cluster2, inr.cluster3}
	clustersWithoutNamespace := []*management.Cluster{inr.cluster2, inr.cluster3}
	sharedNamespaces, uniqueNamespace, createdUser, userPassword, _, _, statefulset, err := inr.createTestResources(inr.client, allClusters)
	require.NoError(inr.T(), err, "Failed to create test resources")

	sharedNamespaceName := sharedNamespaces[inr.cluster.ID].Name
	deployment, err := deploymentapi.CreateDeployment(inr.client, inr.cluster.ID, uniqueNamespace.Name, "", 1, "", "", false, false, false, true)
	require.NoError(inr.T(), err)

	podList, err := extpodapi.ListPods(inr.client, inr.cluster.ID, uniqueNamespace.Name, metav1.ListOptions{})
	require.NoError(inr.T(), err)
	require.NotEmpty(inr.T(), podList.Items, "No pods found in uniqueNamespace")
	pod := &podList.Items[0]

	log.Info("Create a GlobalRole with regular rules only (no inheritedNamespacedRules).")
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, nil)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verifying initial user permissions for user %s (before adding inheritedNamespacedRules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", uniqueNamespace.Name, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", uniqueNamespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", uniqueNamespace.Name, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", uniqueNamespace.Name, "", false, false))

	log.Infof("Update the GlobalRole to include inheritedNamespacedRules for namespace %s that exists only in cluster1.", uniqueNamespace.Name)
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		uniqueNamespace.Name: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = inheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the uniqueNamespace of cluster1 where it exists.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, uniqueNamespace.Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, uniqueNamespace.Name, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s on cluster %s", expectedRoleName, uniqueNamespace.Name, inr.cluster.Name)

	log.Info("Verify that a RoleBinding is created in the uniqueNamespace of cluster1.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, uniqueNamespace.Name)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, uniqueNamespace.Name, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s on cluster %s", expectedRoleBindingName, uniqueNamespace.Name, inr.cluster.Name)
	require.NotNil(inr.T(), rbs, "RoleBinding %s should exist in namespace %s on cluster %s", expectedRoleBindingName, uniqueNamespace.Name, inr.cluster.Name)

	log.Infof("Verifying updated user permissions for user %s.", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", uniqueNamespace.Name, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", uniqueNamespace.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", uniqueNamespace.Name, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", uniqueNamespace.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", uniqueNamespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", uniqueNamespace.Name, "", false, false))
	for _, cluster := range clustersWithoutNamespace {
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", sharedNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", sharedNamespaceName, "", false, false))
	}
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesMultipleClustersTestSuite) TestUpdateGlobalRoleToAddInheritedNamespacedRulesNonExistentNamespace() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	allClusters := []*management.Cluster{inr.cluster, inr.cluster2, inr.cluster3}
	_, _, createdUser, userPassword, _, _, statefulset, err := inr.createTestResources(inr.client, allClusters)
	require.NoError(inr.T(), err, "Failed to create test resources")

	nonExistentNamespaceName := namegen.AppendRandomString("testns-")

	log.Info("Create a GlobalRole with regular rules only (no inheritedNamespacedRules).")
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, nil)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verifying initial user permissions for user %s (before adding inheritedNamespacedRules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
	for _, cluster := range allClusters {
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))
	}

	log.Infof("Update the GlobalRole to include inheritedNamespacedRules for non-existent namespace %s.", nonExistentNamespaceName)
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		nonExistentNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = inheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Infof("Verifying user permissions for user %s after update.", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	for _, cluster := range allClusters {
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))
	}
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func TestInheritedNamespacedRulesMultipleClustersTestSuite(t *testing.T) {
	suite.Run(t, new(InheritedNamespacedRulesMultipleClustersTestSuite))
}

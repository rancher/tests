//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package globalrolesv2

import (
	"fmt"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	extpodapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/pods"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	statefulsetapi "github.com/rancher/tests/actions/kubeapi/workloads/statefulsets"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type InheritedNamespacedRulesTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (inr *InheritedNamespacedRulesTestSuite) TearDownSuite() {
	inr.session.Cleanup()
}

func (inr *InheritedNamespacedRulesTestSuite) SetupSuite() {
	inr.session = session.NewSession()

	client, err := rancher.NewClient("", inr.session)
	require.NoError(inr.T(), err)
	inr.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(inr.T(), clusterName, "Cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(inr.client, clusterName)
	require.NoError(inr.T(), err, "Error getting cluster ID")
	inr.cluster, err = inr.client.Management.Cluster.ByID(clusterID)
	require.NoError(inr.T(), err)
}

func (inr *InheritedNamespacedRulesTestSuite) createTestResources(client *rancher.Client, cluster *management.Cluster) (*v3.Project, *corev1.Namespace, *v3.User, string, *appsv1.Deployment, *corev1.Pod, *appsv1.StatefulSet, error) {
	log.Info("Creating the required resources for the test.")
	createdProject, err := projectapi.CreateProject(client, cluster.ID)
	require.NoError(inr.T(), err, "Failed to create project")

	namespace, err := namespaceapi.CreateNamespace(client, cluster.ID, createdProject.Name, namegen.AppendRandomString("testns-"), "", nil, nil)
	require.NoError(inr.T(), err, "Failed to create namespace")

	deployment, err := deploymentapi.CreateDeployment(client, cluster.ID, namespace.Name, "", 2, "", "", false, false, false, true)
	require.NoError(inr.T(), err, "Failed to create deployment in namespace %s", namespace.Name)

	pods, err := extpodapi.ListPods(client, cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(inr.T(), err, "Failed to list pods in namespace %s", namespace.Name)
	require.NotEmpty(inr.T(), pods.Items, "No pods found in namespace %s", namespace.Name)
	pod := &pods.Items[0]

	podTemplate := podapi.CreateContainerAndPodTemplate("")
	_, err = statefulsetapi.CreateStatefulSet(client, cluster.ID, namespace.Name, podTemplate, 1, true, "")
	require.NoError(inr.T(), err, "Failed to create statefulset in namespace %s in cluster %s", namespace.Name, cluster.ID)
	statefulset, err := statefulsetapi.CreateStatefulSet(client, extclusterapi.LocalCluster, rbac.DefaultNamespace, podTemplate, 1, true, "")
	require.NoError(inr.T(), err, "Failed to create statefulset in namespace %s in cluster %s", namespace.Name, extclusterapi.LocalCluster)

	createdUser, userPassword, err := userapi.CreateUserWithRoles(client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user")

	return createdProject, namespace, createdUser, userPassword, deployment, pod, statefulset, nil
}

func (inr *InheritedNamespacedRulesTestSuite) TestInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for the namespace %s.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err, "Failed to create GlobalRole")
	log.Infof("GlobalRole %s created successfully.", createdGlobalRole.Name)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the test namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s on cluster %s", expectedRoleName, testNamespaceName, inr.cluster.Name)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err, "Failed to create GlobalRoleBinding")
	grbList, err := inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	require.NotNil(inr.T(), grbList, "GlobalRoleBinding should exist")

	log.Info("Verify that a RoleBinding is created in the test namespace of each downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s on cluster %s", expectedRoleBindingName, testNamespaceName, inr.cluster.Name)
	require.NotNil(inr.T(), rbs, "RoleBinding %s should exist in namespace %s on cluster %s", expectedRoleBindingName, testNamespaceName, inr.cluster.Name)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestInheritedNamespacedRulesNonExistentNamespace() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, _, createdUser, userPassword, _, _, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")

	nonExistentNamespaceName := namegen.AppendRandomString("testns-")

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for non-existent namespace %s.", nonExistentNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		nonExistentNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)

	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Infof("Verifying user permissions for user %s.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestInheritedNamespacedRulesMultipleNamespaces() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespace1, createdUser, userPassword, deployment1, pod1, _, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")

	namespace2, err := namespaceapi.CreateNamespace(inr.client, inr.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns2-"), "", nil, nil)
	require.NoError(inr.T(), err)
	deployment2, err := deploymentapi.CreateDeployment(inr.client, inr.cluster.ID, namespace2.Name, "", 1, "", "", false, false, false, true)
	require.NoError(inr.T(), err, "Failed to create deployment in namespace2")

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespaces %s (pods) and %s (deployments).", namespace1.Name, namespace2.Name)
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		namespace1.Name: rbacapi.PolicyRules["readPods"],
		namespace2.Name: rbacapi.PolicyRules["readDeployments"],
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, nil, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that Roles are created in both namespaces of the downstream cluster.")
	expectedRoleName1 := fmt.Sprintf("%s-%s", createdGlobalRole.Name, namespace1.Name)
	expectedRoleName2 := fmt.Sprintf("%s-%s", createdGlobalRole.Name, namespace2.Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, namespace1.Name, expectedRoleName1, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName1, namespace1.Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, namespace2.Name, expectedRoleName2, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName2, namespace2.Name)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that RoleBindings are created in both namespaces of the downstream cluster.")
	expectedRoleBindingName1 := fmt.Sprintf("%s-%s", grb.Name, namespace1.Name)
	expectedRoleBindingName2 := fmt.Sprintf("%s-%s", grb.Name, namespace2.Name)
	rbs1, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, namespace1.Name, expectedRoleBindingName1)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName1, namespace1.Name)
	require.NotNil(inr.T(), rbs1)
	rbs2, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, namespace2.Name, expectedRoleBindingName2)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName2, namespace2.Name)
	require.NotNil(inr.T(), rbs2)

	log.Infof("Verifying user permissions for user %s.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", namespace1.Name, pod1.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", namespace1.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", namespace2.Name, deployment2.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", namespace2.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", namespace1.Name, deployment1.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", namespace1.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", namespace2.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", namespace1.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", namespace1.Name, pod1.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", namespace2.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", namespace1.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", namespace2.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", namespace2.Name, deployment2.Name, false, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToAddInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Info("Create a GlobalRole with regular rules only (localRules, no inheritedNamespacedRules).")
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
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))

	log.Infof("Update the GlobalRole to include inheritedNamespacedRules for namespace %s.", testNamespaceName)
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = inheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Verify that a RoleBinding is created in the namespace of the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName, testNamespaceName)
	require.NotNil(inr.T(), rbs)

	log.Infof("Verifying updated user permissions for user %s (after adding inheritedNamespacedRules).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToAddInheritedNamespacedRulesNonExistentNamespace() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, _, createdUser, userPassword, _, _, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")

	nonExistentNamespaceName := namegen.AppendRandomString("testns3-")

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
	require.NoError(inr.T(), err)

	log.Infof("Verifying initial user permissions for user %s (before adding inheritedNamespacedRules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))

	log.Infof("Update the GlobalRole to include inheritedNamespacedRules for non-existent namespace %s.", nonExistentNamespaceName)
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		nonExistentNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = inheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)
	log.Info("GlobalRole updated successfully with inheritedNamespacedRules for non-existent namespace.")

	log.Info("Verify that no RoleBinding is created for the non-existent namespace in the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, nonExistentNamespaceName)
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, nonExistentNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should NOT exist since namespace %s does not exist", expectedRoleBindingName, nonExistentNamespaceName)

	log.Info("Verify that no Role is created for the non-existent namespace in the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, nonExistentNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, nonExistentNamespaceName, expectedRoleName, false)
	require.NoError(inr.T(), err, "Role %s should NOT exist since namespace %s does not exist", expectedRoleName, nonExistentNamespaceName)

	log.Infof("Verifying user permissions for user %s after update.", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToRemoveInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that a RoleBinding is created in the namespace of the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	_, err = extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (before removing inheritedNamespacedRules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))

	log.Info("Update the GlobalRole to remove inheritedNamespacedRules entirely.")
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = nil

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)
	log.Info("GlobalRole updated successfully - inheritedNamespacedRules removed.")

	log.Info("Verify that the Role is removed from the namespace of the downstream cluster.")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, false)
	require.NoError(inr.T(), err, "Role %s should be removed from namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Verify that the RoleBinding is removed from the namespace of the downstream cluster.")
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be removed from namespace %s", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (after removing inheritedNamespacedRules).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToRemoveRulesFromInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s with both pods and deployments rules.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verifying user permissions for user %s (before removing deployments rules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))

	log.Info("Update the GlobalRole to remove the deployments rules from inheritedNamespacedRules.")
	updatedInheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: rbacapi.PolicyRules["readPods"],
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = updatedInheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that the Role in the downstream cluster is updated with the correct rules (pods only).")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)
	updatedRole, err := extrbacapi.GetRoleByName(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName)
	require.NoError(inr.T(), err, "Failed to get updated role %s in namespace %s", expectedRoleName, testNamespaceName)
	require.NotNil(inr.T(), updatedRole)
	require.Equal(inr.T(), rbacapi.PolicyRules["readPods"], updatedRole.Rules)

	log.Infof("Verifying user permissions for user %s (after removing deployments rules).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToAddNewRulesToInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s with read pods and deployments rules.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Infof("Verifying user permissions for user %s (before adding delete deployments rule).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))

	log.Info("Update the GlobalRole to add delete deployments rule to inheritedNamespacedRules.")
	updatedInheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...), rbacapi.PolicyRules["deleteDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = updatedInheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that the Role in the downstream cluster is updated with the correct rules.")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)
	updatedRole, err := extrbacapi.GetRoleByName(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName)
	require.NoError(inr.T(), err, "Failed to get updated role %s in namespace %s", expectedRoleName, testNamespaceName)
	require.Contains(inr.T(), updatedRole.Rules, rbacapi.PolicyRules["deleteDeployments"][0])

	log.Infof("Verifying user permissions for user %s (after adding delete deployments rule).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToUpdateRulesInInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s with read pods and deployments rules.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verifying user permissions for user %s (before updating rules).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))

	log.Info("Update the GlobalRole to change deployments rules from 'get, list' to 'get, delete'.")
	updatedInheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["deleteDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = updatedInheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that the Role in the downstream cluster is updated with the correct rules.")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)
	updatedRole, err := extrbacapi.GetRoleByName(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName)
	require.NoError(inr.T(), err, "Failed to get updated role %s in namespace %s", expectedRoleName, testNamespaceName)
	require.NotNil(inr.T(), updatedRole)
	require.Contains(inr.T(), updatedRole.Rules, rbacapi.PolicyRules["deleteDeployments"][0])
	require.NotContains(inr.T(), updatedRole.Rules, rbacapi.PolicyRules["readDeployments"][0])

	log.Infof("Verifying user permissions for user %s (after updating rules).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestDeleteGlobalRoleBindingRemovesRoleBindings() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Info("Verify that a RoleBinding is created in the namespace of the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	_, err = extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (before deleting GlobalRoleBinding).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Info("Delete the GlobalRoleBinding.")
	err = extrbacapi.DeleteGlobalRoleBinding(inr.client, grb.Name, true)
	require.NoError(inr.T(), err, "Failed to delete GlobalRoleBinding %s", grb.Name)

	log.Info("Verify that the RoleBinding is removed from the namespace of the downstream cluster.")
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be removed from namespace %s", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (after deleting GlobalRoleBinding).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", false, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestDeleteGlobalRoleRemovesRoleAndRoleBindings() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Info("Verify that a RoleBinding is created in the namespace of the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName, testNamespaceName)
	require.NotNil(inr.T(), rbs)

	log.Infof("Verifying user permissions for user %s (before deleting GlobalRole).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Info("Delete the GlobalRole.")
	err = inr.client.WranglerContext.Mgmt.GlobalRole().Delete(createdGlobalRole.Name, &metav1.DeleteOptions{})
	require.NoError(inr.T(), err, "Failed to delete GlobalRole %s", createdGlobalRole.Name)

	log.Info("Verify that the Role is removed from the namespace of the downstream cluster.")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, false)
	require.NoError(inr.T(), err, "Role %s should be removed from namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Verify that the RoleBinding is removed from the namespace of the downstream cluster.")
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be removed from namespace %s", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (after deleting GlobalRole).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", false, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToMigrateNamespaceInInheritedNamespacedRules() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	createdProject, namespace1, createdUser, userPassword, deployment1, pod1, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespace1Name := namespace1.Name

	log.Info("Create a second namespace with resources.")
	namespace2, err := namespaceapi.CreateNamespace(inr.client, inr.cluster.ID, createdProject.Name, namegen.AppendRandomString("testns2-"), "", nil, nil)
	require.NoError(inr.T(), err)
	testNamespace2Name := namespace2.Name

	deployment2, err := deploymentapi.CreateDeployment(inr.client, inr.cluster.ID, namespace2.Name, "", 1, "", "", false, false, false, true)
	require.NoError(inr.T(), err, "Failed to create deployment in namespace2")

	pods2, err := extpodapi.ListPods(inr.client, inr.cluster.ID, namespace2.Name, metav1.ListOptions{})
	require.NoError(inr.T(), err, "Failed to list pods in namespace2")
	require.NotEmpty(inr.T(), pods2.Items, "No pods found in namespace2")
	pod2 := &pods2.Items[0]

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s.", testNamespace1Name)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespace1Name: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in namespace1 of the downstream cluster.")
	expectedRoleName1 := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespace1Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespace1Name, expectedRoleName1, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName1, testNamespace1Name)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Info("Verify that a RoleBinding is created in namespace1 of the downstream cluster.")
	expectedRoleBindingName1 := fmt.Sprintf("%s-%s", grb.Name, testNamespace1Name)
	rbs1, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespace1Name, expectedRoleBindingName1)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName1, testNamespace1Name)
	require.NotNil(inr.T(), rbs1)

	log.Infof("Verifying user permissions for user %s (before namespace migration).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespace1Name, pod1.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespace1Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespace1Name, deployment1.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespace1Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespace2Name, pod2.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespace2Name, "", false, false))

	log.Infof("Update the GlobalRole to migrate inheritedNamespacedRules from namespace %s to namespace %s.", testNamespace1Name, testNamespace2Name)
	updatedInheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespace2Name: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedNamespacedRules = updatedInheritedNamespacedRules

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Infof("Verify that the Role is removed from namespace %s.", testNamespace1Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespace1Name, expectedRoleName1, false)
	require.NoError(inr.T(), err, "Role %s should be removed from namespace %s", expectedRoleName1, testNamespace1Name)

	log.Infof("Verify that the RoleBinding is removed from namespace %s.", testNamespace1Name)
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, testNamespace1Name, expectedRoleBindingName1)
	require.NoError(inr.T(), err, "RoleBinding %s should be removed from namespace %s", expectedRoleBindingName1, testNamespace1Name)

	log.Infof("Verify that a Role is created in namespace %s.", testNamespace2Name)
	expectedRoleName2 := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespace2Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespace2Name, expectedRoleName2, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName2, testNamespace2Name)

	log.Infof("Verify that a RoleBinding is created in namespace %s.", testNamespace2Name)
	expectedRoleBindingName2 := fmt.Sprintf("%s-%s", grb.Name, testNamespace2Name)
	rbs2, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespace2Name, expectedRoleBindingName2)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName2, testNamespace2Name)
	require.NotNil(inr.T(), rbs2)

	log.Infof("Verifying user permissions for user %s (after namespace migration).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespace2Name, pod2.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespace2Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespace2Name, deployment2.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespace2Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespace1Name, pod1.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespace1Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespace1Name, deployment1.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespace1Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespace2Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestRoleAndRoleBindingCleanupWhenNamespaceDeleted() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	log.Infof("Verify that a Role is created in the namespace %s.", testNamespaceName)
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Infof("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Infof("Verify that a RoleBinding is created in the namespace %s.", testNamespaceName)
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	_, err = extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "Failed to get rolebinding %s in namespace %s", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (before namespace deletion).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Infof("Delete namespace %s from the downstream cluster.", testNamespaceName)
	err = extnamespaceapi.DeleteNamespace(inr.client, inr.cluster.ID, testNamespaceName, true)
	require.NoError(inr.T(), err, "Failed to delete namespace %s", testNamespaceName)

	log.Info("Verify that the Role is removed from the downstream cluster (since namespace is deleted).")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, false)
	require.NoError(inr.T(), err, "Role %s should be removed since namespace %s was deleted", expectedRoleName, testNamespaceName)

	log.Info("Verify that the RoleBinding is removed from the downstream cluster (since namespace is deleted).")
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be removed since namespace %s was deleted", expectedRoleBindingName, testNamespaceName)

	log.Infof("Verifying user permissions for user %s (after namespace deletion).", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestNamespaceCreatedAfterGlobalRoleBindingExists() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	nonExistentNamespaceName := namegen.AppendRandomString("testns3-")

	log.Info("Create a user and statefulset in local cluster for testing.")
	createdUser, userPassword, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user")

	podTemplate := podapi.CreateContainerAndPodTemplate("")
	statefulset, err := statefulsetapi.CreateStatefulSet(inr.client, extclusterapi.LocalCluster, rbac.DefaultNamespace, podTemplate, 1, true, "")
	require.NoError(inr.T(), err, "Failed to create statefulset in local cluster")

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for non-existent namespace %s.", nonExistentNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		nonExistentNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)

	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRole should exist on local cluster")

	log.Info("Verify that no Role is created for the non-existent namespace in downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, nonExistentNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, nonExistentNamespaceName, expectedRoleName, false)
	require.NoError(inr.T(), err, "Role %s should NOT exist since namespace %s does not exist", expectedRoleName, nonExistentNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Info("Verify that no RoleBinding is created for the non-existent namespace in downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, nonExistentNamespaceName)
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, nonExistentNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should NOT exist since namespace %s does not exist", expectedRoleBindingName, nonExistentNamespaceName)

	log.Infof("Verifying user permissions for user %s (before namespace creation).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", nonExistentNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Infof("Create namespace %s in the downstream cluster after GlobalRoleBinding exists.", nonExistentNamespaceName)
	createdProject, err := projectapi.CreateProject(inr.client, inr.cluster.ID)
	require.NoError(inr.T(), err, "Failed to create project")

	namespace, err := namespaceapi.CreateNamespace(inr.client, inr.cluster.ID, createdProject.Name, nonExistentNamespaceName, "", nil, nil)
	require.NoError(inr.T(), err, "Failed to create namespace %s", nonExistentNamespaceName)

	log.Infof("Create deployment and pods in the newly created namespace %s.", namespace.Name)
	deployment, err := deploymentapi.CreateDeployment(inr.client, inr.cluster.ID, namespace.Name, "", 1, "", "", false, false, false, true)
	require.NoError(inr.T(), err, "Failed to create deployment in namespace %s", namespace.Name)

	pods, err := extpodapi.ListPods(inr.client, inr.cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(inr.T(), err, "Failed to list pods in namespace %s", namespace.Name)
	require.NotEmpty(inr.T(), pods.Items, "No pods found in namespace %s", namespace.Name)
	pod := &pods.Items[0]

	log.Infof("Verify that a Role is created in the newly created namespace %s of the downstream cluster.", namespace.Name)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, namespace.Name, expectedRoleName, true)
	require.NoError(inr.T(), err, "Role %s should be created in namespace %s", expectedRoleName, namespace.Name)

	log.Infof("Verify that a RoleBinding is created in the newly created namespace %s of the downstream cluster.", namespace.Name)
	_, err = extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, namespace.Name, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be created in namespace %s", expectedRoleBindingName, namespace.Name)

	log.Infof("Verifying user permissions for user %s (after namespace creation) in namespace %s.", createdUser.Username, namespace.Name)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", namespace.Name, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", namespace.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", namespace.Name, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", namespace.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", namespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", namespace.Name, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", namespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", namespace.Name, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", namespace.Name, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestEmptyInheritedNamespacedRulesMap() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Info("Create a GlobalRole with an empty inheritedNamespacedRules map.")
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	emptyInheritedNamespacedRules := map[string][]rbacv1.PolicyRule{}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, emptyInheritedNamespacedRules)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRole should exist on local cluster")

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Info("Verify that no namespace-scoped Role is created in the downstream cluster for the test namespace.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, false)
	require.NoError(inr.T(), err, "Role %s should NOT exist since inheritedNamespacedRules is empty", expectedRoleName)

	log.Info("Verify that no namespace-scoped RoleBinding is created in the downstream cluster for the test namespace.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	err = extrbacapi.WaitForRoleBindingDeletion(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should NOT exist since inheritedNamespacedRules is empty", expectedRoleBindingName)

	log.Infof("Verifying user permissions for user %s.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestGlobalRoleBoundToMultipleUsers() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, _, _, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Info("Create multiple users.")
	user1, user1Password, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user1")
	user2, user2Password, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user2")
	user3, user3Password, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user3")

	users := []*v3.User{user1, user2, user3}
	passwords := []string{user1Password, user2Password, user3Password}

	log.Infof("Create a GlobalRole with inheritedNamespacedRules for namespace %s.", testNamespaceName)
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRole should exist on local cluster")

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Failed to get role %s in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create GlobalRoleBindings for each user.")
	grbs := make([]*v3.GlobalRoleBinding, len(users))
	for i, user := range users {
		grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, user.Username, "", "")
		require.NoError(inr.T(), err, "Failed to create GlobalRoleBinding for user %s", user.Username)
		_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
		require.NoError(inr.T(), err, "GlobalRoleBinding for user %s should exist", user.Username)
		grbs[i] = grb
	}

	log.Infof("Verify that RoleBindings are created for each user in the namespace %s.", testNamespaceName)
	for i, grb := range grbs {
		expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
		_, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
		require.NoError(inr.T(), err, "Failed to get RoleBinding %s for user %s", expectedRoleBindingName, users[i].Username)
	}

	log.Infof("Verify permissions for each user in the namespace %s.", testNamespaceName)
	for i, user := range users {
		log.Infof("Verifying permissions for user %s.", user.Username)
		userClient, err := inr.client.AsPublicAPIUser(user, passwords[i])
		require.NoError(inr.T(), err)
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
		require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
	}
}

func (inr *InheritedNamespacedRulesTestSuite) TestCombinedRulesVerification() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, _, _, _, _, _, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources in downstream cluster")
	testNamespaceName := namespace.Name

	log.Info("Create a user.")
	createdUser, userPassword, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err, "Failed to create user")

	log.Info("Create namespace testns-local in local cluster for namespacedRules testing.")
	localProject, err := projectapi.CreateProject(inr.client, extclusterapi.LocalCluster)
	require.NoError(inr.T(), err, "Failed to create project in local cluster")

	localNamespaceName := namegen.AppendRandomString("testns-")
	localNamespace, err := namespaceapi.CreateNamespace(inr.client, extclusterapi.LocalCluster, localProject.Name, localNamespaceName, "", nil, nil)
	require.NoError(inr.T(), err)

	log.Info("Create a statefulset in downstream cluster namespace for testing.")
	podTemplate := podapi.CreateContainerAndPodTemplate("")
	statefulsetDownstream, err := statefulsetapi.CreateStatefulSet(inr.client, inr.cluster.ID, testNamespaceName, podTemplate, 1, true, "")
	require.NoError(inr.T(), err, "Failed to create statefulset in downstream cluster")

	log.Info("Create a statefulset in local cluster for testing.")
	statefulsetLocal, err := statefulsetapi.CreateStatefulSet(inr.client, extclusterapi.LocalCluster, rbac.DefaultNamespace, podTemplate, 1, true, "")
	require.NoError(inr.T(), err, "Failed to create statefulset in local cluster")

	log.Info("Create a GlobalRole with rules + namespacedRules + inheritedNamespacedRules.")
	clusterWideRules := rbacapi.PolicyRules["readStatefulSets"]
	namespacedRules := map[string][]rbacv1.PolicyRule{
		localNamespace.Name: rbacapi.PolicyRules["readPods"],
	}
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: rbacapi.PolicyRules["readStatefulSets"],
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, clusterWideRules, namespacedRules, inheritedNamespacedRules)
	require.NoError(inr.T(), err, "Failed to create GlobalRole")

	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that a Role is created in the downstream cluster namespace from inheritedNamespacedRules.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Role %s should be created in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Verify that a RoleBinding is created in the downstream cluster namespace from inheritedNamespacedRules.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be created in namespace %s", expectedRoleBindingName, testNamespaceName)
	require.NotNil(inr.T(), rbs)

	log.Infof("Verifying user permissions for user %s.", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "pods", localNamespace.Name, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "pods", rbac.DefaultNamespace, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulsetLocal.Namespace, statefulsetLocal.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulsetLocal.Namespace, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "statefulsets", testNamespaceName, statefulsetDownstream.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "statefulsets", testNamespaceName, statefulsetDownstream.Name, false, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestUpdateGlobalRoleToRemoveInheritedClusterRoles() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Info("Create a GlobalRole with inheritedNamespacedRules and inheritedClusterRoles.")
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, []string{rbac.ClusterMember.String()}, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRoleBinding should exist")

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Role %s should be created in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Verify that a RoleBinding is created in the namespace of the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be created in namespace %s", expectedRoleBindingName, testNamespaceName)
	require.NotNil(inr.T(), rbs)

	log.Infof("Verifying initial user permissions for user %s (with inheritedClusterRoles).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Info("Update the GlobalRole to remove inheritedClusterRoles (revoke cluster-member permission).")
	updatedGlobalRole, err := inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)
	updatedGlobalRole.InheritedClusterRoles = []string{}

	_, err = extrbacapi.UpdateGlobalRole(inr.client, updatedGlobalRole)
	require.NoError(inr.T(), err)

	log.Info("Verify that the Role still exists in the namespace of the downstream cluster.")
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Role %s should still exist in namespace %s", expectedRoleName, testNamespaceName)

	log.Infof("Verifying user permissions after removing inheritedClusterRoles for user %s.", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func (inr *InheritedNamespacedRulesTestSuite) TestCRTBDeletionRevokesAccess() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	_, namespace, createdUser, userPassword, deployment, pod, statefulset, err := inr.createTestResources(inr.client, inr.cluster)
	require.NoError(inr.T(), err, "Failed to create test resources")
	testNamespaceName := namespace.Name

	log.Info("Create a GlobalRole with inheritedNamespacedRules but WITHOUT inheritedClusterRoles.")
	localRules := rbacapi.PolicyRules["readStatefulSets"]
	inheritedNamespacedRules := map[string][]rbacv1.PolicyRule{
		testNamespaceName: append(rbacapi.PolicyRules["readPods"], rbacapi.PolicyRules["readDeployments"]...),
	}

	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithAllRules(inr.client, nil, localRules, nil, inheritedNamespacedRules)
	require.NoError(inr.T(), err, "Failed to create GlobalRole")
	log.Infof("GlobalRole %s created successfully.", createdGlobalRole.Name)

	_, err = inr.client.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err, "GlobalRole should exist on local cluster")

	log.Info("Verify that a Role is created in the namespace of the downstream cluster.")
	expectedRoleName := fmt.Sprintf("%s-%s", createdGlobalRole.Name, testNamespaceName)
	err = rbacapi.WaitForRoleExistence(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleName, true)
	require.NoError(inr.T(), err, "Role %s should be created in namespace %s", expectedRoleName, testNamespaceName)

	log.Info("Create a GlobalRoleBinding that binds the GlobalRole to the user.")
	grb, err := rbacapi.CreateGlobalRoleBinding(inr.client, createdGlobalRole.Name, createdUser.Username, "", "")
	require.NoError(inr.T(), err)
	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grb.Name, metav1.GetOptions{})
	require.NoError(inr.T(), err)

	log.Info("Verify that a RoleBinding is created in the namespace of the downstream cluster.")
	expectedRoleBindingName := fmt.Sprintf("%s-%s", grb.Name, testNamespaceName)
	rbs, err := extrbacapi.WaitForRoleBindingToExist(inr.client, inr.cluster.ID, testNamespaceName, expectedRoleBindingName)
	require.NoError(inr.T(), err, "RoleBinding %s should be created in namespace %s", expectedRoleBindingName, testNamespaceName)
	require.NotNil(inr.T(), rbs)

	log.Infof("Verifying initial user permissions for user %s (without inheritedClusterRoles).", createdUser.Username)
	userClient, err := inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Infof("Add user %s as cluster-member to the downstream cluster via CRTB.", createdUser.Username)
	crtb, err := rbacapi.CreateClusterRoleTemplateBinding(inr.client, inr.cluster.ID, createdUser.Username, rbac.ClusterMember.String())
	require.NoError(inr.T(), err, "Failed to create CRTB")

	log.Infof("Verifying user permissions after adding CRTB for user %s.", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))

	log.Infof("Delete the CRTB to remove cluster-member access for user %s.", createdUser.Username)
	err = extrbacapi.DeleteClusterRoleTemplateBinding(inr.client, crtb.Namespace, crtb.Name, true)
	require.NoError(inr.T(), err)

	log.Infof("Verifying user permissions after deleting CRTB for user %s.", createdUser.Username)
	userClient, err = inr.client.AsPublicAPIUser(createdUser, userPassword)
	require.NoError(inr.T(), err)
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "get", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "pods", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "pods", testNamespaceName, pod.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "create", "deployments", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "delete", "deployments", testNamespaceName, deployment.Name, false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, inr.cluster.ID, "list", "statefulsets", testNamespaceName, "", false, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "get", "statefulsets", statefulset.Namespace, statefulset.Name, true, false))
	require.NoError(inr.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, "list", "statefulsets", statefulset.Namespace, "", true, false))
}

func TestInheritedNamespacedRulesTestSuite(t *testing.T) {
	suite.Run(t, new(InheritedNamespacedRulesTestSuite))
}

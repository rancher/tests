//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package aggregatedclusterroles

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/secrets"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AggregatedClusterRolesCrtbTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TearDownSuite() {
	acrc.session.Cleanup()

	log.Infof("Disabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	err := rbacapi.SetAggregatedClusterRoleFeatureFlag(acrc.client, false)
	if err != nil {
		log.Warnf("Failed to disable the feature flag during teardown: %v", err)
	}
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) SetupSuite() {
	acrc.session = session.NewSession()

	client, err := rancher.NewClient("", acrc.session)
	require.NoError(acrc.T(), err)
	acrc.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(acrc.T(), clusterName, "Cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(acrc.client, clusterName)
	require.NoError(acrc.T(), err, "Error getting cluster ID")
	acrc.cluster, err = acrc.client.Management.Cluster.ByID(clusterID)
	require.NoError(acrc.T(), err)

	log.Infof("Enabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	featureEnabled, err := rbacapi.IsFeatureEnabled(acrc.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(acrc.T(), err, "Failed to check if feature flag is enabled")
	if !featureEnabled {
		err := rbacapi.SetAggregatedClusterRoleFeatureFlag(acrc.client, true)
		require.NoError(acrc.T(), err, "Failed to enable the feature flag")
	} else {
		log.Infof("Feature flag %s is already enabled.", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	}
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) acrCreateTestResourcesForCrtb(client *rancher.Client, cluster *management.Cluster) (*v3.Project, []*corev1.Namespace, *management.User, []*appsv1.Deployment, []string, []*corev1.Secret, error) {
	log.Info("Creating the required resources for the test.")
	createdProject, err := projectapi.CreateProject(client, cluster.ID)
	require.NoError(acrc.T(), err, "Failed to create project")

	downstreamContext, err := clusterapi.GetClusterWranglerContext(client, cluster.ID)
	require.NoError(acrc.T(), err, "Failed to get downstream cluster context")

	var createdNamespaces []*corev1.Namespace
	var createdDeployments []*appsv1.Deployment
	var createdSecrets []*corev1.Secret
	var podNames []string

	numNamespaces := 2
	for i := 0; i < numNamespaces; i++ {
		namespace, err := namespaceapi.CreateNamespaceUsingWrangler(client, cluster.ID, createdProject.Name, nil)
		require.NoError(acrc.T(), err, "Failed to create namespace")
		createdNamespaces = append(createdNamespaces, namespace)

		createdDeployment, err := deployment.CreateDeployment(client, cluster.ID, namespace.Name, 2, "", "", false, false, false, true)
		require.NoError(acrc.T(), err, "Failed to create deployment in namespace %s", namespace.Name)
		createdDeployments = append(createdDeployments, createdDeployment)

		podList, err := downstreamContext.Core.Pod().List(namespace.Name, metav1.ListOptions{})
		require.NoError(acrc.T(), err, "Failed to list pods in namespace %s", namespace.Name)
		require.Greater(acrc.T(), len(podList.Items), 0, "No pods found in namespace %s", namespace.Name)
		podNames = append(podNames, podList.Items[0].Name)

		secretData := map[string][]byte{
			"hello": []byte("world"),
		}
		createdSecret, err := secrets.CreateSecret(client, cluster.ID, namespace.Name, secretData, corev1.SecretTypeOpaque, nil, nil)
		require.NoError(acrc.T(), err, "Failed to create secret in namespace %s", namespace.Name)
		createdSecrets = append(createdSecrets, createdSecret)
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String())
	require.NoError(acrc.T(), err, "Failed to create user")

	return createdProject, createdNamespaces, createdUser, createdDeployments, podNames, createdSecrets, nil
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbInheritanceWithClusterMgmtResources() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, _, createdUser, _, _, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates with cluster management plane resources.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := rbacapi.PolicyRules["createProjects"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 8, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbInheritanceWithRegularResources() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates with regular resources.")
	childRules := rbacapi.PolicyRules["readDeployments"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 0, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, false, true))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbInheritanceWithMgmtAndRegularResources() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, childRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbInheritanceWithMultipleRules() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates with multiple rules.")
	chidRules := []rbacv1.PolicyRule{
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{rbacapi.DeploymentsResource},
			APIGroups: []string{rbacapi.AppsAPIGroup},
		},
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{rbacapi.SecretsResource},
			APIGroups: []string{""},
		},
	}
	mainRules := []rbacv1.PolicyRule{
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{rbacapi.PodsResource},
			APIGroups: []string{""},
		},
		{
			Verbs:     []string{"create", "get", "update"},
			Resources: []string{rbacapi.ProjectResource},
			APIGroups: []string{rbacapi.ManagementAPIGroup},
		},
	}
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, chidRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 9, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyProjectMgmtACR(acrc.client, clusterapi.LocalCluster, childRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 2, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "secrets", namespaceName, createdSecret[0].Name, false, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbWithNoInheritance() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating a cluster role template with no inheritance.")
	mainRules := rbacapi.PolicyRules["readProjects"]
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 2, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", createdNamespaces[0].Name, "", false, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbInheritedRulesOnly() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := []rbacv1.PolicyRule{}
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, childRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", createdNamespaces[0].Name, "", false, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbInheritanceWithTwoCrtbs() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates.")
	childRules1 := rbacapi.PolicyRules["readDeployments"]
	childRules2 := rbacapi.PolicyRules["readPods"]
	mainRules1 := rbacapi.PolicyRules["createProjects"]
	mainRules2 := rbacapi.PolicyRules["readProjects"]

	createdChildRT1, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules1, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	createdChildRT2, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules2, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate1 := []*v3.RoleTemplate{createdChildRT1}
	inheritedChildRoleTemplate2 := []*v3.RoleTemplate{createdChildRT2}

	createdMainRT1, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules1, inheritedChildRoleTemplate1, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")
	createdMainRT2, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules2, inheritedChildRoleTemplate2, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName1 := createdChildRT1.Name
	mainRTName1 := createdMainRT1.Name
	childRTName2 := createdChildRT2.Name
	mainRTName2 := createdMainRT2.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName1, mainRTName1, childRTName2, mainRTName2)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 12, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName1, mainRTName1, childRTName2, mainRTName2)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 8, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName1})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName2, []string{childRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName1, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName2, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName1, []string{childRTName1})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName2, []string{childRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName1)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName1)
	require.NoError(acrc.T(), err, "Failed to assign role to user")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName2)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName2)
	require.NoError(acrc.T(), err, "Failed to assign role to user")

	log.Infof("Verifying cluster role template bindings created for user %s", createdUser.Username)
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 2)
	require.NoError(acrc.T(), err, "CRTBs not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[1], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[1], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbNestedInheritance() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, podNames, createdSecret, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating nested cluster role templates.")
	childRules1 := rbacapi.PolicyRules["createProjects"]
	childRules2 := rbacapi.PolicyRules["readSecrets"]
	childRules3 := rbacapi.PolicyRules["readProjects"]
	mainRules1 := rbacapi.PolicyRules["readPods"]

	createdChildRT1, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules1, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate1 := []*v3.RoleTemplate{createdChildRT1}
	createdChildRT2, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules2, inheritedChildRoleTemplate1, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate2 := []*v3.RoleTemplate{createdChildRT2}
	createdChildRT3, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules3, inheritedChildRoleTemplate2, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate3 := []*v3.RoleTemplate{createdChildRT3}
	createdMainRT1, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules1, inheritedChildRoleTemplate3, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName1 := createdChildRT1.Name
	childRTName2 := createdChildRT2.Name
	childRTName3 := createdChildRT3.Name
	mainRTName1 := createdMainRT1.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName1, childRTName2, childRTName3, mainRTName1)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 18, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName1, childRTName2, childRTName3, mainRTName1)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 8, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName1, childRTName3})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyProjectMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName1, childRTName2, childRTName3})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for main role")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, childRTName3, []string{childRTName1, childRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for child role 3")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, childRTName2, []string{childRTName1})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for child role 2")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName1, []string{childRTName1, childRTName2, childRTName3})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR for main role")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, childRTName3, []string{childRTName1, childRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR for child role 3")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, childRTName2, []string{childRTName1})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR for child role 2")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName1)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName1)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 2, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbMultipleLevelsOfInheritance() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating multiple levels of nested cluster role templates.")
	childRules11 := rbacapi.PolicyRules["readDeployments"]
	childRules12 := rbacapi.PolicyRules["readProjects"]
	parentRules1 := rbacapi.PolicyRules["readNamespaces"]
	childRules21 := rbacapi.PolicyRules["readPods"]
	parentRules2 := rbacapi.PolicyRules["readSecrets"]
	mainRules1 := rbacapi.PolicyRules["createProjects"]

	createdChildRT11, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules11, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template 11")

	createdChildRT12, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules12, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template 12")

	inheritedParentRoleTemplate1 := []*v3.RoleTemplate{createdChildRT11, createdChildRT12}
	createdParentRT1, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, parentRules1, inheritedParentRoleTemplate1, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create parent role template 1")

	createdChildRT21, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules21, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template 21")

	inheritedParentRoleTemplate2 := []*v3.RoleTemplate{createdChildRT21}
	createdParentRT2, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, parentRules2, inheritedParentRoleTemplate2, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create parent role template 2")

	inheritedMainRoleTemplate1 := []*v3.RoleTemplate{createdParentRT1, createdParentRT2}
	createdMainRT1, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules1, inheritedMainRoleTemplate1, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template 1")

	childRTName11 := createdChildRT11.Name
	childRTName12 := createdChildRT12.Name
	parentRTName1 := createdParentRT1.Name
	childRTName21 := createdChildRT21.Name
	parentRTName2 := createdParentRT2.Name
	mainRTName1 := createdMainRT1.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2, mainRTName1)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 20, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2, mainRTName1)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 12, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName12})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyProjectMgmtACR(acrc.client, clusterapi.LocalCluster, parentRTName2, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for main role")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName1, []string{childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR for main role")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName1)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName1)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 2, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "namespaces", "", namespaceName, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "namespaces", "", "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbUpdateRoleTemplateToAddInheritance() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, podNames, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating a cluster role template with no inheritance.")
	mainRules := rbacapi.PolicyRules["readProjects"]
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 2, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))

	log.Info("Creating a new cluster role template.")
	childRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")

	childRTName := createdChildRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 2, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 2, len(downstreamCRs.Items))

	log.Info("Updating the main role template to add inheritance.")
	updatedMainRT, err := rbacapi.UpdateRoleTemplateInheritance(acrc.client, mainRTName, []*v3.RoleTemplate{createdChildRT})
	require.NoError(acrc.T(), err, "Failed to update role template inheritance")

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{childRTName})
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, updatedMainRT.Name, []string{childRTName})
	require.NoError(acrc.T(), err)

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbUpdateRoleTemplateToRemoveInheritance() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbacapi.PolicyRules["readPods"]
	mainRules := rbacapi.PolicyRules["readProjects"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 6, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrc.client, acrc.cluster.ID, childRTName, mainRTName)
	require.NoError(acrc.T(), err)
	require.Equal(acrc.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrc.T(), err, "Failed to fetch local ACR for cluster-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrc.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Info("Removing inheritance from the main role template.")
	updatedMainRT, err := rbacapi.UpdateRoleTemplateInheritance(acrc.client, mainRTName, []*v3.RoleTemplate{})
	require.NoError(acrc.T(), err, "Failed to update role template inheritance")

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyClusterMgmtACR(acrc.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyMainACRContainsAllRules(acrc.client, acrc.cluster.ID, updatedMainRT.Name, []string{})
	require.NoError(acrc.T(), err)

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
}

func (acrc *AggregatedClusterRolesCrtbTestSuite) TestCrtbVerifyCrossClusterAccessRestriction() {
	subSession := acrc.session.NewSession()
	defer subSession.Cleanup()

	createdProject, _, createdUser, _, _, _, err := acrc.acrCreateTestResourcesForCrtb(acrc.client, acrc.cluster)
	require.NoError(acrc.T(), err)
	createdProject2, err := projectapi.CreateProject(acrc.client, clusterapi.LocalCluster)
	require.NoError(acrc.T(), err, "Failed to create project")

	log.Info("Creating cluster role templates with cluster management plane resources.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := rbacapi.PolicyRules["createProjects"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrc.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrc.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name
	log.Infof("Adding user %s to the downstream cluster %s with role %s", createdUser.Username, acrc.cluster.ID, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, acrc.cluster.ID, createdUser, mainRTName)
	require.NoError(acrc.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrc.client, createdUser.ID, 1)
	require.NoError(acrc.T(), err, "CRTB not found for user")

	log.Infof("Adding user %s to the local cluster with role %s", createdUser.Username, rbac.ManageNodes.String())
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrc.client, clusterapi.LocalCluster, createdUser, rbac.ManageNodes.String())
	require.NoError(acrc.T(), err, "Failed to assign role to user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrc.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrc.client, acrc.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrc.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "create", "projects", "", "", true, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "update", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "patch", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, acrc.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, clusterapi.LocalCluster, createdUser, "get", "projects", "", createdProject2.Name, false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, clusterapi.LocalCluster, createdUser, "list", "projects", "", "", false, true))
	require.NoError(acrc.T(), rbacapi.VerifyUserPermission(acrc.client, clusterapi.LocalCluster, createdUser, "create", "projects", "", "", false, true))
}

func TestAggregatedClusterRolesCrtbTestSuite(t *testing.T) {
	suite.Run(t, new(AggregatedClusterRolesCrtbTestSuite))
}

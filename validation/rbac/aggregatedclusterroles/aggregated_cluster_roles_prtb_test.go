//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

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
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/projects"
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

type AggregatedClusterRolesPrtbTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TearDownSuite() {
	acrp.session.Cleanup()
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) SetupSuite() {
	acrp.session = session.NewSession()

	client, err := rancher.NewClient("", acrp.session)
	require.NoError(acrp.T(), err)
	acrp.client = client

	log.Info("Getting cluster name from the config file and append cluster details in acrp")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(acrp.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(acrp.client, clusterName)
	require.NoError(acrp.T(), err, "Error getting cluster ID")
	acrp.cluster, err = acrp.client.Management.Cluster.ByID(clusterID)
	require.NoError(acrp.T(), err)
}

func acrCreateTestResourcesForPrtb(client *rancher.Client, cluster *management.Cluster) (*v3.Project, []*corev1.Namespace, *management.User, []*appsv1.Deployment, []string, []*corev1.Secret, error) {
	createdProject, err := projects.CreateProjectUsingWrangler(client, cluster.ID)
	require.NoError(nil, err, "Failed to create project")

	downstreamContext, err := clusterapi.GetClusterWranglerContext(client, cluster.ID)
	require.NoError(nil, err, "Failed to get downstream cluster context")

	var createdNamespaces []*corev1.Namespace
	var createdDeployments []*appsv1.Deployment
	var createdSecrets []*corev1.Secret
	var podNames []string

	numNamespaces := 2
	for i := 0; i < numNamespaces; i++ {
		namespace, err := projects.CreateNamespaceUsingWrangler(client, cluster.ID, createdProject.Name, nil)
		require.NoError(nil, err, "Failed to create namespace")
		createdNamespaces = append(createdNamespaces, namespace)

		createdDeployment, err := deployment.CreateDeployment(client, cluster.ID, namespace.Name, 2, "", "", false, false, false, true)
		require.NoError(nil, err, "Failed to create deployment in namespace %s", namespace.Name)
		createdDeployments = append(createdDeployments, createdDeployment)

		podList, err := downstreamContext.Core.Pod().List(namespace.Name, metav1.ListOptions{})
		require.NoError(nil, err, "Failed to list pods in namespace %s", namespace.Name)
		require.Greater(nil, len(podList.Items), 0, "No pods found in namespace %s", namespace.Name)
		podNames = append(podNames, podList.Items[0].Name)

		secretData := map[string][]byte{
			"hello": []byte("world"),
		}
		createdSecret, err := secrets.CreateSecret(client, cluster.ID, namespace.Name, secretData, corev1.SecretTypeOpaque, nil, nil)
		require.NoError(nil, err, "Failed to create secret in namespace %s", namespace.Name)
		createdSecrets = append(createdSecrets, createdSecret)
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String())
	require.NoError(nil, err, "Failed to create user")

	return createdProject, createdNamespaces, createdUser, createdDeployments, podNames, createdSecrets, nil
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithProjectMgmtResources() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with project management plane resources.")
	childRules := rbac.PolicyRules["readPrtbs"]
	mainRules := rbac.PolicyRules["updatePrtbs"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithRegularResources() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with regular resources.")
	childRules := rbac.PolicyRules["readDeployments"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithMgmtAndRegularResources() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbac.PolicyRules["readPrtbs"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 7, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, childRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithMultipleRules() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with multiple rules.")
	chidRules := []rbacv1.PolicyRule{
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{"deployments"},
			APIGroups: []string{rbac.AppsAPIGroup},
		},
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{"secrets"},
			APIGroups: []string{""},
		},
	}
	mainRules := []rbacv1.PolicyRule{
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{"pods"},
			APIGroups: []string{""},
		},
		{
			Verbs:     []string{"get", "list", "update"},
			Resources: []string{"projectroletemplatebindings"},
			APIGroups: []string{rbac.ManagementAPIGroup},
		},
	}
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, chidRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "secrets", namespaceName, createdSecret[0].Name, false, false))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbWithNoInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating a project role template with no inheritance.")
	mainRules := rbac.PolicyRules["readPrtbs"]
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", createdNamespaces[0].Name, "", false, false))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritedRulesOnly() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates.")
	childRules := rbac.PolicyRules["readPrtbs"]
	mainRules := []rbacv1.PolicyRule{}
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 7, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, childRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", createdNamespaces[0].Name, "", false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithTwoPrtbs() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates.")
	childRules1 := rbac.PolicyRules["readDeployments"]
	childRules2 := rbac.PolicyRules["readPods"]
	mainRules1 := rbac.PolicyRules["updatePrtbs"]
	mainRules2 := rbac.PolicyRules["readPrtbs"]

	createdChildRT1, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules1, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	createdChildRT2, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules2, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate1 := []*v3.RoleTemplate{createdChildRT1}
	inheritedChildRoleTemplate2 := []*v3.RoleTemplate{createdChildRT2}

	createdMainRT1, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules1, inheritedChildRoleTemplate1, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")
	createdMainRT2, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules2, inheritedChildRoleTemplate2, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName1 := createdChildRT1.Name
	mainRTName1 := createdMainRT1.Name
	childRTName2 := createdChildRT2.Name
	mainRTName2 := createdMainRT2.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName1, mainRTName1, childRTName2, mainRTName2)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 12, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName1, mainRTName1, childRTName2, mainRTName2)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName1, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName2, []string{childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName1, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName2, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName1, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName2, []string{childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName1)
	createdPrtb1, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName1)
	require.NoError(acrp.T(), err, "Failed to assign role to user")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName2)
	createdPrtb2, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName2)
	require.NoError(acrp.T(), err, "Failed to assign role to user")

	log.Info("Verifying project role template bindings are created for the user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 2)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[1], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[1], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb1.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb2.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbNestedInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating nested project role templates.")
	childRules1 := rbac.PolicyRules["readDeployments"]
	childRules2 := rbac.PolicyRules["readSecrets"]
	childRules3 := rbac.PolicyRules["readPrtbs"]
	mainRules1 := rbac.PolicyRules["readPods"]

	createdChildRT1, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules1, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate1 := []*v3.RoleTemplate{createdChildRT1}
	createdChildRT2, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules2, inheritedChildRoleTemplate1, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate2 := []*v3.RoleTemplate{createdChildRT2}
	createdChildRT3, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules3, inheritedChildRoleTemplate2, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedMainRoleTemplate1 := []*v3.RoleTemplate{createdChildRT3}
	createdMainRT1, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules1, inheritedMainRoleTemplate1, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName1 := createdChildRT1.Name
	childRTName2 := createdChildRT2.Name
	childRTName3 := createdChildRT3.Name
	mainRTName1 := createdMainRT1.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName1, childRTName2, childRTName3, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 13, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName1, childRTName2, childRTName3, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(downstreamCRs.Items))

	log.Info("Verifying ACRs in the local cluster.")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, childRTName3, []string{childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName1, []string{childRTName1, childRTName2, childRTName3})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for main role")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, childRTName3, []string{childRTName1, childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for child role 3")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, childRTName2, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for child role 2")

	log.Info("Verifying ACRs in the downstream cluster.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName1, []string{childRTName1, childRTName2, childRTName3})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for main role")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, childRTName3, []string{childRTName1, childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for child role 3")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, childRTName2, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for child role 2")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName1)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName1)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbMultipleLevelsOfInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating multiple levels of nested project role templates.")
	childRules11 := rbac.PolicyRules["readDeployments"]
	childRules12 := rbac.PolicyRules["readSecrets"]
	parentRules1 := rbac.PolicyRules["readNamespaces"]
	childRules21 := rbac.PolicyRules["readPods"]
	parentRules2 := rbac.PolicyRules["readPrtbs"]
	mainRules1 := rbac.PolicyRules["updatePrtbs"]

	createdChildRT11, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules11, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template 11")

	createdChildRT12, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules12, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template 12")

	inheritedParentRoleTemplate1 := []*v3.RoleTemplate{createdChildRT11, createdChildRT12}
	createdParentRT1, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, parentRules1, inheritedParentRoleTemplate1, false, nil)
	require.NoError(acrp.T(), err, "Failed to create parent role template 1")

	createdChildRT21, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules21, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template 21")

	inheritedParentRoleTemplate2 := []*v3.RoleTemplate{createdChildRT21}
	createdParentRT2, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, parentRules2, inheritedParentRoleTemplate2, false, nil)
	require.NoError(acrp.T(), err, "Failed to create parent role template 2")

	inheritedMainRoleTemplate1 := []*v3.RoleTemplate{createdParentRT1, createdParentRT2}
	createdMainRT1, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules1, inheritedMainRoleTemplate1, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template 1")

	childRTName11 := createdChildRT11.Name
	childRTName12 := createdChildRT12.Name
	parentRTName1 := createdParentRT1.Name
	childRTName21 := createdChildRT21.Name
	parentRTName2 := createdParentRT2.Name
	mainRTName1 := createdMainRT1.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 19, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 12, len(downstreamCRs.Items))

	log.Info("Verifying ACRs in the local cluster.")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName1, []string{parentRTName2, childRTName12})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName1, []string{childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for main role")

	log.Info("Verifying ACRs in the downstream cluster.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName1, []string{childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for main role")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName1)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName1)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "namespaces", "", namespaceName, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbUpdateRoleTemplateToAddInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, podNames, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating a project role template with no inheritance.")
	mainRules := rbac.PolicyRules["readPrtbs"]
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))

	log.Info("Creating a new project role template.")
	childRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	childRTName := createdChildRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(downstreamCRs.Items))

	log.Info("Updating the main role template to add inheritance.")
	updatedMainRT, err := rbac.UpdateRoleTemplateInheritance(acrp.client, mainRTName, []*v3.RoleTemplate{createdChildRT})
	require.NoError(acrp.T(), err, "Failed to update role template inheritance")

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, updatedMainRT.Name, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, updatedMainRT.Name, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	// GH Issue: https://github.com/rancher/rancher/issues/52525
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources after updating inheritance.")
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbUpdateRoleTemplateToRemoveInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, podNames, _, err := acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbac.PolicyRules["readPods"]
	mainRules := rbac.PolicyRules["readPrtbs"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrp.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying Aggregated Cluster Role (ACR) for mainRole in both the local and downstream clusters includes all the rules from both childRole and mainRole.")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Info("Removing inheritance from the main role template.")
	updatedMainRT, err := rbac.UpdateRoleTemplateInheritance(acrp.client, mainRTName, []*v3.RoleTemplate{})
	require.NoError(acrp.T(), err, "Failed to update role template inheritance")

	log.Info("Verifying ACR for main role in the local and downstream clusters includes all rules.")
	err = rbac.VerifyProjectMgmtACR(acrp.client, rbacapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrp.T(), err)
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, rbacapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrp.T(), err)
	err = rbac.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, updatedMainRT.Name, []string{})
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrp.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrp.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	// GH Issue: https://github.com/rancher/rancher/issues/52525
	err = rbac.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Info("Verifying user access to the resources after updating inheritance.")
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbac.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func TestAggregatedClusterRolesPrtbTestSuite(t *testing.T) {
	suite.Run(t, new(AggregatedClusterRolesPrtbTestSuite))
}

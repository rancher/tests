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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type AggregatedClusterRolesCleanupTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TearDownSuite() {
	acrd.session.Cleanup()
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) SetupSuite() {
	acrd.session = session.NewSession()

	client, err := rancher.NewClient("", acrd.session)
	require.NoError(acrd.T(), err)
	acrd.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(acrd.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(acrd.client, clusterName)
	require.NoError(acrd.T(), err, "Error getting cluster ID")
	acrd.cluster, err = acrd.client.Management.Cluster.ByID(clusterID)
	require.NoError(acrd.T(), err)
}

func acrCreateTestResourcesForCleanup(client *rancher.Client, cluster *management.Cluster) (*v3.Project, []*corev1.Namespace, *management.User, []*appsv1.Deployment, []string, []*corev1.Secret, error) {
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

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestDeleteRoleTemplateRemovesClusterRoles() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a cluster role template with no inheritance.")
	mainRules := rbac.PolicyRules["readProjects"]
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(mainRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbDeleteRoleTemplateWithInheritance() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbac.PolicyRules["readProjects"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbac.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(mainRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	// Need to confirm with Jonathan if this is accurate - may be as designed because the role template handler does not delete the binding
	// log.Info("Verifying ClusterRoleTemplateBinding is deleted for the user")
	// _, err = rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	// require.NoError(acrd.T(), err, "crtb still exists for the user")

	// log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	// err = rbac.VerifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 0)
	// require.NoError(acrd.T(), err)

	// log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	// err = rbac.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0)
	// require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Deleting the role template %s.", childRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(childRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbDeleteRoleTemplateWithInheritance() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbac.PolicyRules["readPrtbs"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(mainRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	// Need to confirm with Jonathan if this is accurate - may be as designed because the role template handler does not delete the binding
	// log.Info("Verifying ProjectRoleTemplateBinding is deleted for the user")
	// _, err = rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	// require.NoError(acrd.T(), err, "prtb still exists for the user")

	// log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	// err = rbac.VerifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], 0, 0)
	// require.NoError(acrd.T(), err)

	// log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	// err = rbac.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], 0, 0)
	// require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Deleting the role template %s.", childRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(childRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbRemoveUserFromCluster() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbac.PolicyRules["readProjects"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbac.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Removing user %s from downstream cluster.", createdUser.ID)
	err = rbac.DeleteClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, crtbs[0].Name)
	require.NoError(acrd.T(), err, "Failed to delete role template")
	_, err = rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	_, err = rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying user access to the resources.")
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbRemoveUserFromCluster() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbac.PolicyRules["readPrtbs"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Removing user %s from the project %s in the downstream cluster.", createdUser.ID, createdProject.Name)
	err = rbac.DeleteProjectRoleTemplateBinding(acrd.client, createdProject.Name, prtbs[0].Name)
	require.NoError(acrd.T(), err, "Failed to delete role template")
	_, err = rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "PRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 5, len(downstreamCRs.Items))

	log.Info("Verifying user access to the resources.")
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbUserDeletionCleansUpAllBindings() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	_, _, createdUser, _, _, _, err := acrCreateTestResourcesForCrtb(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbac.PolicyRules["readProjects"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local and downstream clusters.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbac.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local and downstream clusters.")
	err = rbac.VerifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)
	err = rbac.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Infof("Deleting user %s", createdUser.ID)
	err = acrd.client.WranglerContext.Mgmt.User().Delete(createdUser.ID, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete user")

	log.Info("Verifying Rancher automatically cleaned up CRTB.")
	_, err = rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "Expected no CRTBs for deleted user")

	log.Info("Verifying RBAC bindings are removed from local and downstream clusters.")
	require.NoError(acrd.T(), rbac.VerifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 0))
	require.NoError(acrd.T(), rbac.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0))

	log.Info("Verifying the cluster roles created in the local and downstream clusters still exist.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbUserDeletionCleansUpAllBindings() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrCreateTestResourcesForPrtb(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbac.PolicyRules["readPrtbs"]
	mainRules := rbac.PolicyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local and downstream clusters.")
	localCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	_, err = rbac.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local and downstream clusters.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	require.NoError(acrd.T(), err)
	err = rbac.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Infof("Deleting user %s", createdUser.ID)
	err = acrd.client.WranglerContext.Mgmt.User().Delete(createdUser.ID, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete user")

	log.Info("Verifying Rancher automatically cleaned up PRTB.")
	_, err = rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "Expected no CRTBs for deleted user")

	log.Info("Verifying the role bindings are removed from local and downstream clusters.")
	err = rbac.VerifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 0)
	require.NoError(acrd.T(), err)
	err = rbac.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local and downstream clusters persist.")
	localCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err = rbac.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))
}

func TestAggregatedClusterRolesCleanupTestSuite(t *testing.T) {
	suite.Run(t, new(AggregatedClusterRolesCleanupTestSuite))
}

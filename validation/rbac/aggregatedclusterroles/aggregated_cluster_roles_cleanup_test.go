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
	userapi "github.com/rancher/tests/actions/users"
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

	log.Infof("Disabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	err := rbacapi.SetAggregatedClusterRoleFeatureFlag(acrd.client, false)
	if err != nil {
		log.Warnf("Failed to disable the feature flag during teardown: %v", err)
	}
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) SetupSuite() {
	acrd.session = session.NewSession()

	client, err := rancher.NewClient("", acrd.session)
	require.NoError(acrd.T(), err)
	acrd.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(acrd.T(), clusterName, "Cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(acrd.client, clusterName)
	require.NoError(acrd.T(), err, "Error getting cluster ID")
	acrd.cluster, err = acrd.client.Management.Cluster.ByID(clusterID)
	require.NoError(acrd.T(), err)

	log.Infof("Enabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	featureEnabled, err := rbacapi.IsFeatureEnabled(acrd.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(acrd.T(), err, "Failed to check if feature flag is enabled")
	if !featureEnabled {
		err := rbacapi.SetAggregatedClusterRoleFeatureFlag(acrd.client, true)
		require.NoError(acrd.T(), err, "Failed to enable the feature flag")
	} else {
		log.Infof("Feature flag %s is already enabled.", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	}
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) acrCreateTestResourcesForCleanup(client *rancher.Client, cluster *management.Cluster) (*v3.Project, []*corev1.Namespace, *management.User, []*appsv1.Deployment, []string, []*corev1.Secret, error) {
	log.Info("Creating the required resources for the test.")
	createdProject, err := projectapi.CreateProject(client, cluster.ID)
	require.NoError(acrd.T(), err, "Failed to create project")

	downstreamContext, err := clusterapi.GetClusterWranglerContext(client, cluster.ID)
	require.NoError(acrd.T(), err, "Failed to get downstream cluster context")

	var createdNamespaces []*corev1.Namespace
	var createdDeployments []*appsv1.Deployment
	var createdSecrets []*corev1.Secret
	var podNames []string

	numNamespaces := 2
	for i := 0; i < numNamespaces; i++ {
		namespace, err := namespaceapi.CreateNamespaceUsingWrangler(client, cluster.ID, createdProject.Name, nil)
		require.NoError(acrd.T(), err, "Failed to create namespace")
		createdNamespaces = append(createdNamespaces, namespace)

		createdDeployment, err := deployment.CreateDeployment(client, cluster.ID, namespace.Name, 2, "", "", false, false, false, true)
		require.NoError(acrd.T(), err, "Failed to create deployment in namespace %s", namespace.Name)
		createdDeployments = append(createdDeployments, createdDeployment)

		podList, err := downstreamContext.Core.Pod().List(namespace.Name, metav1.ListOptions{})
		require.NoError(acrd.T(), err, "Failed to list pods in namespace %s", namespace.Name)
		require.Greater(acrd.T(), len(podList.Items), 0, "No pods found in namespace %s", namespace.Name)
		podNames = append(podNames, podList.Items[0].Name)

		secretData := map[string][]byte{
			"hello": []byte("world"),
		}
		createdSecret, err := secrets.CreateSecret(client, cluster.ID, namespace.Name, secretData, corev1.SecretTypeOpaque, nil, nil)
		require.NoError(acrd.T(), err, "Failed to create secret in namespace %s", namespace.Name)
		createdSecrets = append(createdSecrets, createdSecret)
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String())
	require.NoError(acrd.T(), err, "Failed to create user")

	return createdProject, createdNamespaces, createdUser, createdDeployments, podNames, createdSecrets, nil
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestDeleteRoleTemplateRemovesClusterRoles() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a cluster role template with no inheritance.")
	mainRules := rbacapi.PolicyRules["readProjects"]
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, mainRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = rbacapi.DeleteRoleTemplate(acrd.client, mainRTName)
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbDeleteRoleTemplateWithInheritance() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrd.acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = rbacapi.DeleteRoleTemplate(acrd.client, mainRTName)
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Deleting the role template %s.", childRTName)
	err = rbacapi.DeleteRoleTemplate(acrd.client, childRTName)
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbDeleteRoleTemplateWithInheritance() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrd.acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "PRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = rbacapi.DeleteRoleTemplate(acrd.client, mainRTName)
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Deleting the role template %s.", childRTName)
	err = rbacapi.DeleteRoleTemplate(acrd.client, childRTName)
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbRemoveUserFromCluster() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrd.acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Removing user %s from the downstream cluster.", createdUser.ID)
	err = rbacapi.DeleteClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, crtbs[0].Name)
	require.NoError(acrd.T(), err, "Failed to delete role template")
	_, err = rbacapi.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "CRTB still exists for the user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, clusterapi.LocalCluster, &crtbs[0], 0, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbRemoveUserFromProject() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrd.acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "PRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Removing user %s from the project %s in the downstream cluster.", createdUser.ID, createdProject.Name)
	err = rbacapi.DeleteProjectRoleTemplateBinding(acrd.client, createdPrtb.Namespace, createdPrtb.Name)
	require.NoError(acrd.T(), err, "Failed to delete project role template binding")
	_, err = rbacapi.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "PRTB still exists for the user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 0, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", false, true))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbacapi.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbUserDeletionCleansUpAllBindings() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	_, _, createdUser, _, _, _, err := acrd.acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := rbacapi.PolicyRules["readProjects"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, childRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ClusterContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to the downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, clusterapi.LocalCluster, &crtbs[0], 1, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Infof("Deleting user %s", createdUser.ID)
	err = userapi.DeleteUser(acrd.client, createdUser.ID)
	require.NoError(acrd.T(), err, "Failed to delete user")

	log.Infof("Verifying that the cluster role template binding for user %s is automatically deleted.", createdUser.Username)
	_, err = rbacapi.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "CRTB still exists for the deleted user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	require.NoError(acrd.T(), rbacapi.VerifyBindingsForCrtb(acrd.client, clusterapi.LocalCluster, &crtbs[0], 0, 0))
	require.NoError(acrd.T(), rbacapi.VerifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0))

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbUserDeletionCleansUpAllBindings() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrd.acrCreateTestResourcesForCleanup(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrd.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "PRTB not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Infof("Deleting user %s", createdUser.ID)
	err = userapi.DeleteUser(acrd.client, createdUser.ID)
	require.NoError(acrd.T(), err, "Failed to delete user")

	log.Infof("Verifying that the project role template binding for user %s is automatically deleted.", createdUser.Username)
	_, err = rbacapi.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	require.NoError(acrd.T(), err, "PRTB still exists for the deleted user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 0, 0)
	require.NoError(acrd.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))
}

func TestAggregatedClusterRolesCleanupTestSuite(t *testing.T) {
	suite.Run(t, new(AggregatedClusterRolesCleanupTestSuite))
}

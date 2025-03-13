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

type AggregatedClusterRolesPrtbTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TearDownSuite() {
	acrp.session.Cleanup()

	log.Infof("Disabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	err := rbacapi.SetAggregatedClusterRoleFeatureFlag(acrp.client, false)
	if err != nil {
		log.Warnf("Failed to disable the feature flag during teardown: %v", err)
	}
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) SetupSuite() {
	acrp.session = session.NewSession()

	client, err := rancher.NewClient("", acrp.session)
	require.NoError(acrp.T(), err)
	acrp.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(acrp.T(), clusterName, "Cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(acrp.client, clusterName)
	require.NoError(acrp.T(), err, "Error getting cluster ID")
	acrp.cluster, err = acrp.client.Management.Cluster.ByID(clusterID)
	require.NoError(acrp.T(), err)

	log.Infof("Enabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	featureEnabled, err := rbacapi.IsFeatureEnabled(acrp.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(acrp.T(), err, "Failed to check if feature flag is enabled")
	if !featureEnabled {
		err := rbacapi.SetAggregatedClusterRoleFeatureFlag(acrp.client, true)
		require.NoError(acrp.T(), err, "Failed to enable the feature flag")
	} else {
		log.Infof("Feature flag %s is already enabled.", rbacapi.AggregatedRoleTemplatesFeatureFlag)
	}
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) acrCreateTestResourcesForPrtb(client *rancher.Client, cluster *management.Cluster) (*v3.Project, []*corev1.Namespace, *management.User, []*appsv1.Deployment, []string, []*corev1.Secret, error) {
	log.Info("Creating the required resources for the test.")
	createdProject, err := projectapi.CreateProject(client, cluster.ID)
	require.NoError(acrp.T(), err, "Failed to create project")

	downstreamContext, err := clusterapi.GetClusterWranglerContext(client, cluster.ID)
	require.NoError(acrp.T(), err, "Failed to get downstream cluster context")

	var createdNamespaces []*corev1.Namespace
	var createdDeployments []*appsv1.Deployment
	var createdSecrets []*corev1.Secret
	var podNames []string

	numNamespaces := 2
	for i := 0; i < numNamespaces; i++ {
		namespace, err := namespaceapi.CreateNamespaceUsingWrangler(client, cluster.ID, createdProject.Name, nil)
		require.NoError(acrp.T(), err, "Failed to create namespace")
		createdNamespaces = append(createdNamespaces, namespace)

		createdDeployment, err := deployment.CreateDeployment(client, cluster.ID, namespace.Name, 2, "", "", false, false, false, true)
		require.NoError(acrp.T(), err, "Failed to create deployment in namespace %s", namespace.Name)
		createdDeployments = append(createdDeployments, createdDeployment)

		podList, err := downstreamContext.Core.Pod().List(namespace.Name, metav1.ListOptions{})
		require.NoError(acrp.T(), err, "Failed to list pods in namespace %s", namespace.Name)
		require.Greater(acrp.T(), len(podList.Items), 0, "No pods found in namespace %s", namespace.Name)
		podNames = append(podNames, podList.Items[0].Name)

		secretData := map[string][]byte{
			"hello": []byte("world"),
		}
		createdSecret, err := secrets.CreateSecret(client, cluster.ID, namespace.Name, secretData, corev1.SecretTypeOpaque, nil, nil)
		require.NoError(acrp.T(), err, "Failed to create secret in namespace %s", namespace.Name)
		createdSecrets = append(createdSecrets, createdSecret)
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String())
	require.NoError(acrp.T(), err, "Failed to create user")

	return createdProject, createdNamespaces, createdUser, createdDeployments, podNames, createdSecrets, nil
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithProjectMgmtResources() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with project management plane resources.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := rbacapi.PolicyRules["updatePrtbs"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithRegularResources() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with regular resources.")
	childRules := rbacapi.PolicyRules["readDeployments"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 0, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithMgmtAndRegularResources() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, childRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithMultipleRules() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with multiple rules.")
	chidRules := []rbacv1.PolicyRule{
		{
			Verbs:     []string{"get", "list"},
			Resources: []string{"deployments"},
			APIGroups: []string{rbacapi.AppsAPIGroup},
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
			APIGroups: []string{rbacapi.ManagementAPIGroup},
		},
	}
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, chidRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "secrets", namespaceName, createdSecret[0].Name, false, false))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbWithNoInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating a project role template with no inheritance.")
	mainRules := rbacapi.PolicyRules["readPrtbs"]
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", createdNamespaces[0].Name, "", false, false))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritedRulesOnly() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := []rbacv1.PolicyRule{}
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 7, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, childRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", createdNamespaces[0].Name, "", false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbInheritanceWithTwoPrtbs() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates.")
	childRules1 := rbacapi.PolicyRules["readDeployments"]
	childRules2 := rbacapi.PolicyRules["readPods"]
	mainRules1 := rbacapi.PolicyRules["updatePrtbs"]
	mainRules2 := rbacapi.PolicyRules["readPrtbs"]

	createdChildRT1, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules1, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	createdChildRT2, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules2, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate1 := []*v3.RoleTemplate{createdChildRT1}
	inheritedChildRoleTemplate2 := []*v3.RoleTemplate{createdChildRT2}

	createdMainRT1, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules1, inheritedChildRoleTemplate1, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")
	createdMainRT2, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules2, inheritedChildRoleTemplate2, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName1 := createdChildRT1.Name
	mainRTName1 := createdMainRT1.Name
	childRTName2 := createdChildRT2.Name
	mainRTName2 := createdMainRT2.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName1, mainRTName1, childRTName2, mainRTName2)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 12, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName1, mainRTName1, childRTName2, mainRTName2)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName2, []string{childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName1, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName2, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName1, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName2, []string{childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName1)
	createdPrtb1, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName1)
	require.NoError(acrp.T(), err, "Failed to assign role to user")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName2)
	createdPrtb2, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName2)
	require.NoError(acrp.T(), err, "Failed to assign role to user")

	log.Infof("Verifying project role template bindings are created for user %s", createdUser.Username)
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 2)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtb1Namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtb1Namespace}, 1, 0)
	require.NoError(acrp.T(), err)

	prtb2Namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[1].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[1], []*corev1.Namespace{prtb2Namespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[1], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb1.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb1.Namespace, createdPrtb1.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb2.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbNestedInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating nested project role templates.")
	childRules1 := rbacapi.PolicyRules["readDeployments"]
	childRules2 := rbacapi.PolicyRules["readSecrets"]
	childRules3 := rbacapi.PolicyRules["readPrtbs"]
	mainRules1 := rbacapi.PolicyRules["readPods"]

	createdChildRT1, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules1, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate1 := []*v3.RoleTemplate{createdChildRT1}
	createdChildRT2, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules2, inheritedChildRoleTemplate1, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedChildRoleTemplate2 := []*v3.RoleTemplate{createdChildRT2}
	createdChildRT3, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules3, inheritedChildRoleTemplate2, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	inheritedMainRoleTemplate1 := []*v3.RoleTemplate{createdChildRT3}
	createdMainRT1, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules1, inheritedMainRoleTemplate1, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName1 := createdChildRT1.Name
	childRTName2 := createdChildRT2.Name
	childRTName3 := createdChildRT3.Name
	mainRTName1 := createdMainRT1.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName1, childRTName2, childRTName3, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 13, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName1, childRTName2, childRTName3, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 8, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster roles in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, childRTName3, []string{childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName1, childRTName2, childRTName3})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for main role")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, childRTName3, []string{childRTName1, childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for child role 3")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, childRTName2, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for child role 2")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName1, []string{childRTName1, childRTName2, childRTName3})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for main role")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, childRTName3, []string{childRTName1, childRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for child role 3")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, childRTName2, []string{childRTName1})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for child role 2")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName1)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName1)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbMultipleLevelsOfInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, createdSecret, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating multiple levels of nested project role templates.")
	childRules11 := rbacapi.PolicyRules["readDeployments"]
	childRules12 := rbacapi.PolicyRules["readSecrets"]
	parentRules1 := rbacapi.PolicyRules["readNamespaces"]
	childRules21 := rbacapi.PolicyRules["readPods"]
	parentRules2 := rbacapi.PolicyRules["readPrtbs"]
	mainRules1 := rbacapi.PolicyRules["updatePrtbs"]

	createdChildRT11, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules11, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template 11")

	createdChildRT12, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules12, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template 12")

	inheritedParentRoleTemplate1 := []*v3.RoleTemplate{createdChildRT11, createdChildRT12}
	createdParentRT1, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, parentRules1, inheritedParentRoleTemplate1, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create parent role template 1")

	createdChildRT21, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules21, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template 21")

	inheritedParentRoleTemplate2 := []*v3.RoleTemplate{createdChildRT21}
	createdParentRT2, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, parentRules2, inheritedParentRoleTemplate2, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create parent role template 2")

	inheritedMainRoleTemplate1 := []*v3.RoleTemplate{createdParentRT1, createdParentRT2}
	createdMainRT1, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules1, inheritedMainRoleTemplate1, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template 1")

	childRTName11 := createdChildRT11.Name
	childRTName12 := createdChildRT12.Name
	parentRTName1 := createdParentRT1.Name
	childRTName21 := createdChildRT21.Name
	parentRTName2 := createdParentRT2.Name
	mainRTName1 := createdMainRT1.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 19, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2, mainRTName1)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 12, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName1, []string{parentRTName2, childRTName12})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName1, []string{childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for main role")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName1, []string{childRTName11, childRTName12, parentRTName1, childRTName21, parentRTName2})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR for main role")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName1)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName1)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "deployments", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "namespaces", "", namespaceName, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "secrets", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbUpdateRoleTemplateToAddInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, podNames, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating a project role template with no inheritance.")
	mainRules := rbacapi.PolicyRules["readPrtbs"]
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))

	log.Info("Creating a new project role template.")
	childRules := rbacapi.PolicyRules["readPods"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")

	childRTName := createdChildRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(localCRs.Items))
	downstreamCRs, err = rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 2, len(downstreamCRs.Items))

	log.Info("Updating the main role template to add inheritance.")
	updatedMainRT, err := rbacapi.UpdateRoleTemplateInheritance(acrp.client, mainRTName, []*v3.RoleTemplate{createdChildRT})
	require.NoError(acrp.T(), err, "Failed to update role template inheritance")

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, updatedMainRT.Name, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbUpdateRoleTemplateToRemoveInheritance() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, podNames, _, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := rbacapi.PolicyRules["readPods"]
	mainRules := rbacapi.PolicyRules["readPrtbs"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles in the local and downstream clusters.")
	localCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, clusterapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 6, len(localCRs.Items))
	downstreamCRs, err := rbacapi.GetClusterRolesForRoleTemplates(acrp.client, acrp.cluster.ID, childRTName, mainRTName)
	require.NoError(acrp.T(), err)
	require.Equal(acrp.T(), 4, len(downstreamCRs.Items))

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch local ACR")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, mainRTName, nil)
	require.NoError(acrp.T(), err, "Failed to fetch local ACR for project-mgmt resources")
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, mainRTName, []string{childRTName})
	require.NoError(acrp.T(), err, "Failed to fetch downstream ACR")

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Info("Removing inheritance from the main role template.")
	updatedMainRT, err := rbacapi.UpdateRoleTemplateInheritance(acrp.client, mainRTName, []*v3.RoleTemplate{})
	require.NoError(acrp.T(), err, "Failed to update role template inheritance")

	log.Info("Verifying that the aggregated cluster role in the local and downstream clusters includes the correct rules.")
	err = rbacapi.VerifyProjectMgmtACR(acrp.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, clusterapi.LocalCluster, updatedMainRT.Name, []string{})
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyMainACRContainsAllRules(acrp.client, acrp.cluster.ID, updatedMainRT.Name, []string{})
	require.NoError(acrp.T(), err)

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "pods", namespaceName, "", false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
}

func (acrp *AggregatedClusterRolesPrtbTestSuite) TestPrtbVerifyCrossClusterAndProjectAccessRestriction() {
	subSession := acrp.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, _, _, createdSecret, err := acrp.acrCreateTestResourcesForPrtb(acrp.client, acrp.cluster)
	require.NoError(acrp.T(), err)

	createdProject2, err := projectapi.CreateProject(acrp.client, acrp.cluster.ID)
	require.NoError(acrp.T(), err, "Failed to create project")
	createdNamespace2, err := namespaceapi.CreateNamespaceUsingWrangler(acrp.client, acrp.cluster.ID, createdProject2.Name, nil)
	require.NoError(acrp.T(), err, "Failed to create namespace")
	secretData := map[string][]byte{
		"hello": []byte("world"),
	}
	createdSecret2, err := secrets.CreateSecret(acrp.client, acrp.cluster.ID, createdNamespace2.Name, secretData, corev1.SecretTypeOpaque, nil, nil)
	require.NoError(acrp.T(), err, "Failed to create secret in namespace %s", createdNamespace2.Name)

	createdProject3, err := projectapi.CreateProject(acrp.client, clusterapi.LocalCluster)
	require.NoError(acrp.T(), err, "Failed to create project")

	log.Info("Creating project role templates with project management plane resources.")
	childRules := rbacapi.PolicyRules["readPrtbs"]
	mainRules := rbacapi.PolicyRules["updatePrtbs"]
	createdChildRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, childRules, nil, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbacapi.CreateRoleTemplate(acrp.client, rbacapi.ProjectContext, mainRules, inheritedChildRoleTemplate, false, false, nil)
	require.NoError(acrp.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name
	log.Infof("Adding user %s to a project %s in the downstream cluster %s with role %s", createdUser.Username, createdProject.Name, acrp.cluster.Name, mainRTName)
	createdPrtb, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject, mainRTName)
	require.NoError(acrp.T(), err, "Failed to assign role to user")
	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(acrp.client, createdUser.ID, 1)
	require.NoError(acrp.T(), err, "prtb not found for user")

	log.Infof("Adding user %s to a project %s in the downstream cluster %s with role %s", createdUser.Username, createdProject2.Name, acrp.cluster.Name, rbac.SecretsView.String())
	createdPrtb2, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject2, rbac.SecretsView.String())
	require.NoError(acrp.T(), err, "Failed to assign role to user")

	log.Infof("Adding user %s to a project %s in the local cluster with role %s", createdUser.Username, createdProject3.Name, rbac.SecretsView.String())
	createdPrtb3, err := rbacapi.CreateProjectRoleTemplateBinding(acrp.client, createdUser, createdProject3, rbac.SecretsView.String())
	require.NoError(acrp.T(), err, "Failed to assign role to user")

	log.Infof("Verifying role bindings and cluster role bindings for user %s in the local and downstream clusters.", createdUser.Username)
	prtbNamespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{
			Name: prtbs[0].Namespace,
		},
	}
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, clusterapi.LocalCluster, &prtbs[0], []*corev1.Namespace{prtbNamespace}, 1, 0)
	require.NoError(acrp.T(), err)
	err = rbacapi.VerifyBindingsForPrtb(acrp.client, acrp.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrp.T(), err)

	log.Infof("Verifying user permissions for user %s are correct.", createdUser.Username)
	namespaceName := createdNamespaces[0].Name
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb.Namespace, "", true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", namespaceName, createdSecret[0].Name, false, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "list", "projectroletemplatebindings", createdPrtb2.Namespace, "", false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "update", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "patch", "projectroletemplatebindings", createdPrtb2.Namespace, createdPrtb2.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, acrp.cluster.ID, createdUser, "get", "secrets", createdNamespace2.Name, createdSecret2.Name, true, false))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, clusterapi.LocalCluster, createdUser, "get", "projectroletemplatebindings", createdPrtb3.Namespace, createdPrtb3.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, clusterapi.LocalCluster, createdUser, "list", "projectroletemplatebindings", createdPrtb3.Namespace, "", false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, clusterapi.LocalCluster, createdUser, "update", "projectroletemplatebindings", createdPrtb3.Namespace, createdPrtb3.Name, false, true))
	require.NoError(acrp.T(), rbacapi.VerifyUserPermission(acrp.client, clusterapi.LocalCluster, createdUser, "patch", "projectroletemplatebindings", createdPrtb3.Namespace, createdPrtb3.Name, false, true))
}

func TestAggregatedClusterRolesPrtbTestSuite(t *testing.T) {
	suite.Run(t, new(AggregatedClusterRolesPrtbTestSuite))
}

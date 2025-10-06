//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12

package aggregatedclusterroles

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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

	log.Info("Getting cluster name from the config file and append cluster details in acrd")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(acrd.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(acrd.client, clusterName)
	require.NoError(acrd.T(), err, "Error getting cluster ID")
	acrd.cluster, err = acrd.client.Management.Cluster.ByID(clusterID)
	require.NoError(acrd.T(), err)
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestDeleteRoleTemplateRemovesClusterRoles() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Creating a cluster role template with no inheritance.")
	mainRules := policyRules["readProjects"]
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(mainRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbDeleteRoleTemplateWithInheritance() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResources(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := policyRules["readProjects"]
	mainRules := policyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbac.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// err = verifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 1)
	// require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = verifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projects", "", createdProject.Name, false, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(mainRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	// Need to confirm with Jonathan if this is accurate - may be as designed because the role template handler does not delete the binding
	// log.Info("Verifying ClusterRoleTemplateBinding is deleted for the user")
	// _, err = rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	// require.NoError(acrd.T(), err, "crtb still exists for the user")

	// log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	// err = verifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 0)
	// require.NoError(acrd.T(), err)

	// log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	// err = verifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0)
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
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestPrtbDeleteRoleTemplateWithInheritance() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResources(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := policyRules["readPrtbs"]
	mainRules := policyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// err = verifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	// require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = verifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "pods", namespaceName, podNames[0], true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "pods", namespaceName, "", true, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "pods", namespaceName, podNames[0], false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "deployments", namespaceName, createdDeployment[0].Name, false, false))
	require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "delete", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, false, true))

	log.Infof("Deleting the role template %s.", mainRTName)
	err = acrd.client.WranglerContext.Mgmt.RoleTemplate().Delete(mainRTName, &metav1.DeleteOptions{})
	require.NoError(acrd.T(), err, "Failed to delete role template")

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 2, len(downstreamCRs.Items))

	// Need to confirm with Jonathan if this is accurate - may be as designed because the role template handler does not delete the binding
	// log.Info("Verifying ProjectRoleTemplateBinding is deleted for the user")
	// _, err = rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 0)
	// require.NoError(acrd.T(), err, "prtb still exists for the user")

	// log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	// err = verifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], 0, 0)
	// require.NoError(acrd.T(), err)

	// log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	// err = verifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], 0, 0)
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
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 0, len(downstreamCRs.Items))
}

func (acrd *AggregatedClusterRolesCleanupTestSuite) TestCrtbRemoveUserFromCluster() {
	subSession := acrd.session.NewSession()
	defer subSession.Cleanup()

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResources(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating cluster role templates with cluster management and regular resources.")
	childRules := policyRules["readProjects"]
	mainRules := policyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ClusterContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to downstream cluster with role %s", createdUser.Username, mainRTName)
	_, err = rbac.CreateClusterRoleTemplateBinding(acrd.client, acrd.cluster.ID, createdUser, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	crtbs, err := rbac.VerifyClusterRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "CRTB not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// err = verifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 1)
	// require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = verifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 1)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projects", "", createdProject.Name, true, true))
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projects", "", "", true, true))
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

	log.Info("Verifying role bindings and cluster role bindings for the user in the local cluster.")
	err = verifyBindingsForCrtb(acrd.client, rbac.LocalCluster, &crtbs[0], 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	err = verifyBindingsForCrtb(acrd.client, acrd.cluster.ID, &crtbs[0], 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
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

	createdProject, createdNamespaces, createdUser, createdDeployment, podNames, _, err := acrCreateTestResources(acrd.client, acrd.cluster)
	require.NoError(acrd.T(), err)

	log.Info("Creating project role templates with project management and regular resources.")
	childRules := policyRules["readPrtbs"]
	mainRules := policyRules["readPods"]
	createdChildRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, childRules, nil, false, nil)
	require.NoError(acrd.T(), err, "Failed to create child role template")
	inheritedChildRoleTemplate := []*v3.RoleTemplate{createdChildRT}
	createdMainRT, err := rbac.CreateRoleTemplate(acrd.client, rbac.ProjectContext, mainRules, inheritedChildRoleTemplate, false, nil)
	require.NoError(acrd.T(), err, "Failed to create main role template")

	childRTName := createdChildRT.Name
	mainRTName := createdMainRT.Name

	log.Info("Verifying the cluster roles created in the local cluster.")
	localCRs, err := getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 6, len(localCRs.Items))

	log.Info("Verifying the cluster roles created in the downstream cluster.")
	downstreamCRs, err := getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 4, len(downstreamCRs.Items))

	log.Infof("Adding user %s to a project %s in the downstream cluster with role %s", createdUser.Username, createdProject.Name, mainRTName)
	createdPrtb, err := rbac.CreateProjectRoleTemplateBinding(acrd.client, createdUser, createdProject, mainRTName)
	require.NoError(acrd.T(), err, "Failed to assign role to user")
	prtbs, err := rbac.VerifyProjectRoleTemplateBindingForUser(acrd.client, createdUser.ID, 1)
	require.NoError(acrd.T(), err, "prtb not found for user")

	log.Info("Verifying role bindings and cluster role bindings created for the user in the local cluster.")
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// err = verifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 1)
	// require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings created for the user in the downstream cluster.")
	err = verifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 1, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying user access to the resources.")
	namespaceName := createdNamespaces[0].Name
	// Product Bug: https://github.com/rancher/rancher/issues/49224
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "get", "projectroletemplatebindings", createdPrtb.Namespace, createdPrtb.Name, true, true))
	// require.NoError(acrd.T(), rbac.VerifyUserPermission(acrd.client, acrd.cluster.ID, createdUser, "list", "projectroletemplatebindings", "", "", true, true))
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
	err = verifyBindingsForPrtb(acrd.client, rbac.LocalCluster, &prtbs[0], nil, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying role bindings and cluster role bindings for the user in the downstream cluster.")
	err = verifyBindingsForPrtb(acrd.client, acrd.cluster.ID, &prtbs[0], createdNamespaces, 0, 0)
	require.NoError(acrd.T(), err)

	log.Info("Verifying the cluster roles in the local cluster.")
	localCRs, err = getClusterRolesForRoleTemplates(acrd.client, rbacapi.LocalCluster, childRTName, mainRTName)
	require.NoError(acrd.T(), err)
	require.Equal(acrd.T(), 7, len(localCRs.Items))

	log.Info("Verifying the cluster roles in the downstream cluster.")
	downstreamCRs, err = getClusterRolesForRoleTemplates(acrd.client, acrd.cluster.ID, childRTName, mainRTName)
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

func TestAggregatedClusterRolesCleanupTestSuite(t *testing.T) {
	suite.Run(t, new(AggregatedClusterRolesCleanupTestSuite))
}

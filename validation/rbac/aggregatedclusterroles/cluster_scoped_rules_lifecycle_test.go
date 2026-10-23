//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package aggregatedclusterroles

import (
	"context"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extfeaturesapi "github.com/rancher/shepherd/extensions/kubeapi/features"
	extprojectapi "github.com/rancher/shepherd/extensions/kubeapi/projects"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	extdeploymentapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/deployments"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	deploymentapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type ClusterScopedRulesLifecycleTestSuite struct {
	suite.Suite
	client                *rancher.Client
	session               *session.Session
	cluster               *management.Cluster
	featureEnabledBySuite bool
}

func (s *ClusterScopedRulesLifecycleTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)
	s.client = client

	log.Info("Getting cluster name from the config file and append cluster details to the test suite struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(s.T(), clusterName, "Cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(s.client, clusterName)
	require.NoError(s.T(), err, "Error getting cluster ID")
	s.cluster, err = s.client.Management.Cluster.ByID(clusterID)
	require.NoError(s.T(), err)

	featureEnabled, err := extfeaturesapi.IsFeatureEnabled(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(s.T(), err, "Failed to check if the feature flag is enabled")
	if !featureEnabled {
		log.Infof("Enabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
		err := extfeaturesapi.EnableFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
		require.NoError(s.T(), err, "Failed to enable the feature flag")
		s.featureEnabledBySuite = true
	}
}

func (s *ClusterScopedRulesLifecycleTestSuite) TearDownSuite() {
	s.session.Cleanup()

	if s.featureEnabledBySuite {
		log.Infof("Disabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
		err := extfeaturesapi.DisableFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
		if err != nil {
			log.Warnf("Failed to disable the feature flag during teardown: %v", err)
		}
	}
}

func (s *ClusterScopedRulesLifecycleTestSuite) createTestResources(client *rancher.Client, clusterID string, rules, clusterScopedRules []rbacv1.PolicyRule) (*v3.Project, *corev1.Namespace, *v3.User, *rancher.Client, *v3.RoleTemplate, *v3.ProjectRoleTemplateBinding, error) {
	project, namespace, err := projectapi.CreateProjectAndNamespace(client, clusterID)
	require.NoError(s.T(), err, "Failed to create project and namespace")

	user, userClient, err := userapi.CreateUserWithRolesAndClient(client, rbac.StandardUser.String())
	require.NoError(s.T(), err, "Failed to create user and client")

	roleTemplate, err := extrbacapi.CreateRoleTemplate(client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("scoped-rt")},
		Context:            rbacapi.ProjectContext,
		Rules:              rules,
		ClusterScopedRules: clusterScopedRules,
	})
	require.NoError(s.T(), err, "Failed to create role template")

	prtb, err := rbacapi.CreateProjectRoleTemplateBinding(client, user.Name, project, roleTemplate.Name)
	require.NoError(s.T(), err, "Failed to create project role template binding")

	return project, namespace, user, userClient, roleTemplate, prtb, err
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestUpdateExistingRoleTemplateToAddClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that adding clusterScopedRules to an existing project RoleTemplate grants cluster-scoped access")

	_, namespace, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], nil)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.PodsResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))

	updated := roleTemplate.DeepCopy()
	updated.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.UpdateRoleTemplate(s.client, updated)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, updated.Name, user.Name, updated.ClusterScopedRules)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestExpandAndReducePermissions() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that expanding and reducing clusterScopedRules updates effective permissions")

	_, namespace, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, "", "", true))
	updated := roleTemplate.DeepCopy()
	updated.ClusterScopedRules = append([]rbacv1.PolicyRule{{APIGroups: []string{rbacapi.AppsAPIGroup}, Resources: []string{rbacapi.DeploymentsResource}, Verbs: []string{rbacapi.CreateVerb}}}, rbacapi.PolicyRules["writePods"]...)
	updated, err = extrbacapi.UpdateRoleTemplate(s.client, updated)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, updated.Name, user.Name, updated.ClusterScopedRules)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.CreateVerb, rbacapi.AppsAPIGroup, rbacapi.DeploymentsResource, "", "", true))

	updated.ClusterScopedRules = rbacapi.PolicyRules["writePods"]
	_, err = extrbacapi.UpdateRoleTemplate(s.client, updated)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, updated.Name, user.Name, rbacapi.PolicyRules["writePods"])
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.CreateVerb, rbacapi.AppsAPIGroup, rbacapi.DeploymentsResource, "", "", false))
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestRemoveRulesPreservesProjectAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that removing clusterScopedRules revokes cluster access but preserves project access")

	_, namespace, user, userClient, roleTemplate, binding, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	roleNames, _, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.PodsResource, "", "", true))

	updated := roleTemplate.DeepCopy()
	updated.ClusterScopedRules = []rbacv1.PolicyRule{}
	_, err = extrbacapi.UpdateRoleTemplate(s.client, updated)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyBindingsForPrtb(s.client, s.cluster.ID, binding, []*corev1.Namespace{namespace}, 1, 0)
	require.NoError(s.T(), err)

	for _, roleName := range roleNames {
		err = rbacapi.WaitForClusterRoleExistence(s.client, s.cluster.ID, roleName, false)
		require.NoError(s.T(), err)
	}

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, updated.Name, user.Name, nil)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestDeleteOneBindingPreservesOtherUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that deleting a project role template binding revokes only that user's cluster-scoped access")

	project, namespace, user, userClient, roleTemplate, binding, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	controlRoleTemplate, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("scoped-control-rt")},
		Context:    rbacapi.ProjectContext,
		Rules:      rbacapi.PolicyRules["readConfigMaps"],
	})
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, controlRoleTemplate.Name)
	require.NoError(s.T(), err)

	secondProject, err := projectapi.CreateProject(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	secondUser, secondClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, secondUser.Name, secondProject, roleTemplate.Name)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, secondUser.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
		require.NoError(s.T(), rbacapi.VerifyUserCanI(secondClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(secondClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	err = extrbacapi.DeleteProjectRoleTemplateBinding(s.client, binding.Namespace, binding.Name, true)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyBindingsForPrtb(s.client, s.cluster.ID, binding, []*corev1.Namespace{namespace}, 0, 0)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, nil)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, secondUser.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
		require.NoError(s.T(), rbacapi.VerifyUserCanI(secondClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(secondClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestDeleteRoleTemplateRemovesClusterRoles() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that deleting a RoleTemplate removes its generated ClusterRoles")

	_, _, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	expectedRules := append(append([]rbacv1.PolicyRule{}, rbacapi.PolicyRules["readConfigMaps"]...), rbacapi.PolicyRules["allPods"]...)
	for _, clusterID := range []string{extclusterapi.LocalCluster, s.cluster.ID} {
		err = rbacapi.VerifyRoleTemplateClusterRoleRules(s.client, clusterID, roleTemplate.Name, expectedRules)
		require.NoError(s.T(), err, "RoleTemplate rules should be generated in cluster %s", clusterID)
	}

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	err = extrbacapi.DeleteRoleTemplate(s.client, roleTemplate.Name, true)
	require.NoError(s.T(), err)

	_, err = rbacapi.GetRoleTemplateByName(s.client, roleTemplate.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "deleted RoleTemplate must not exist: %v", err)

	for _, clusterID := range []string{extclusterapi.LocalCluster, s.cluster.ID} {
		err = rbacapi.WaitForRoleTemplateClusterRolesDeletion(s.client, clusterID, roleTemplate.Name)
		require.NoError(s.T(), err, "RoleTemplate ClusterRoles should be deleted from cluster %s", clusterID)
	}

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestDeleteProjectRevokesUserAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that deleting one project revokes its user's access but preserves another project's access")

	project, namespace, user, userClient, roleTemplate, binding, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	secondProject, err := projectapi.CreateProject(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	secondUser, secondClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	secondBinding, err := rbacapi.CreateProjectRoleTemplateBinding(s.client, secondUser.Name, secondProject, roleTemplate.Name)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, secondUser.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	err = extprojectapi.DeleteProject(s.client, s.cluster.ID, project.Name, true)
	require.NoError(s.T(), err)

	err = extrbacapi.WaitForProjectRoleTemplateBindingDeletion(s.client, binding.Namespace, binding.Name)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyBindingsForPrtb(s.client, s.cluster.ID, binding, []*corev1.Namespace{namespace}, 0, 0)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, nil)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, secondUser.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	_, err = s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Get(secondBinding.Namespace, secondBinding.Name, metav1.GetOptions{})
	require.NoError(s.T(), err)

	_, err = extprojectapi.GetProjectByName(s.client, s.cluster.ID, secondProject.Name)
	require.NoError(s.T(), err)

	_, err = userClient.WranglerContext.Mgmt.Cluster().Get(s.cluster.ID, metav1.GetOptions{})
	require.True(s.T(), apierrors.IsForbidden(err), "removed project user should lose cluster access: %v", err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(secondClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(secondClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestReduceVerbsRevokesAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that reducing verbs revokes only the removed pod permissions")

	project, namespace, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["writePods"])
	require.NoError(s.T(), err)

	secondProject, secondNamespace, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), project.Name, secondProject.Name)

	for _, namespaceName := range []string{namespace.Name, secondNamespace.Name} {
		for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
			require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, namespaceName, "", true))
		}
	}

	updated := roleTemplate.DeepCopy()
	updated.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.UpdateRoleTemplate(s.client, updated)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, updated.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	for _, namespaceName := range []string{namespace.Name, secondNamespace.Name} {
		for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
			allowed := verb == rbacapi.GetVerb || verb == rbacapi.ListVerb
			require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, namespaceName, "", allowed))
		}
		require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceName, "", true, false))
	}
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestFeatureFlagDisabledAndReenabled() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the feature flag grants, revokes, and restores cluster-wide access while preserving project-scoped access")

	originalEnabled, err := extfeaturesapi.IsFeatureEnabled(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(s.T(), err)
	defer func() {
		err := extfeaturesapi.UpdateFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag, originalEnabled)
		require.NoError(s.T(), err, "Failed to restore aggregated-roletemplates feature flag")
	}()

	err = extfeaturesapi.UpdateFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag, true)
	require.NoError(s.T(), err)
	enabled, err := extfeaturesapi.IsFeatureEnabled(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(s.T(), err)
	require.True(s.T(), enabled)

	_, namespace, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.WatchVerb, rbacapi.CreateVerb, rbacapi.UpdateVerb, rbacapi.PatchVerb, rbacapi.DeleteVerb, rbacapi.DeleteCollectionVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))

	err = extfeaturesapi.UpdateFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag, false)
	require.NoError(s.T(), err)
	enabled, err = extfeaturesapi.IsFeatureEnabled(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(s.T(), err)
	require.False(s.T(), enabled)

	err = rbacapi.VerifyRoleTemplateResourceRules(s.client, s.cluster.ID, roleTemplate.Name, "", rbacapi.PodsResource, false)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, nil)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.WatchVerb, rbacapi.CreateVerb, rbacapi.UpdateVerb, rbacapi.PatchVerb, rbacapi.DeleteVerb, rbacapi.DeleteCollectionVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))

	err = extfeaturesapi.UpdateFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag, true)
	require.NoError(s.T(), err)
	enabled, err = extfeaturesapi.IsFeatureEnabled(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
	require.NoError(s.T(), err)
	require.True(s.T(), enabled)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.WatchVerb, rbacapi.CreateVerb, rbacapi.UpdateVerb, rbacapi.PatchVerb, rbacapi.DeleteVerb, rbacapi.DeleteCollectionVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestZPermissionsSurviveRancherRestart() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that cluster scoped permissions survive Rancher restart")

	_, _, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	roleNamesBefore, bindingNamesBefore, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	rolesBefore, err := rbacapi.GetClusterRolesForRoleTemplates(s.client, s.cluster.ID, roleTemplate.Name)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	err = deploymentapi.RestartDeployment(s.client, extclusterapi.LocalCluster, deploymentapi.RancherDeploymentNamespace, deploymentapi.RancherDeploymentName)
	require.NoError(s.T(), err)
	err = extdeploymentapi.WaitForDeploymentActive(s.client, extclusterapi.LocalCluster, deploymentapi.RancherDeploymentNamespace, deploymentapi.RancherDeploymentName)
	require.NoError(s.T(), err)

	roleNamesAfter, bindingNamesAfter, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), roleNamesBefore, roleNamesAfter, "restart must not create duplicate or orphaned roles")
	require.ElementsMatch(s.T(), bindingNamesBefore, bindingNamesAfter, "restart must not create duplicate or orphaned bindings")

	rolesAfter, err := rbacapi.GetClusterRolesForRoleTemplates(s.client, s.cluster.ID, roleTemplate.Name)
	require.NoError(s.T(), err)
	require.Len(s.T(), rolesAfter.Items, len(rolesBefore.Items))

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		allowed := verb == rbacapi.GetVerb || verb == rbacapi.ListVerb
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", allowed))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestMultiLevelInheritanceClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules on a leaf RoleTemplate propagate through a three-level inheritance chain (A -> B -> C) when the chain is bound")

	leaf, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("chain-c")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	middle, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{leaf}, false, false, nil)
	require.NoError(s.T(), err)

	root, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{middle}, false, false, nil)
	require.NoError(s.T(), err)

	project, _, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, root.Name)
	require.NoError(s.T(), err)

	roleNames, bindingNames, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, root.Name, user.Name, leaf.ClusterScopedRules)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), bindingNames)

	_, err = rbacapi.VerifyExactClusterRoleRules(s.client, s.cluster.ID, leaf.Name+"-cluster-scoped", leaf.ClusterScopedRules)
	require.NoError(s.T(), err)
	_, err = rbacapi.VerifyExactClusterRoleRules(s.client, s.cluster.ID, middle.Name+"-cluster-scoped-aggregator", leaf.ClusterScopedRules)
	require.NoError(s.T(), err)
	rootAggregator, err := rbacapi.VerifyExactClusterRoleRules(s.client, s.cluster.ID, root.Name+"-cluster-scoped-aggregator", leaf.ClusterScopedRules)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), rootAggregator.AggregationRule)
	require.Contains(s.T(), roleNames, rootAggregator.Name)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestSwitchToClusterContextAfterClearingClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a project RoleTemplate can switch to cluster context only after clearing clusterScopedRules")

	created, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("switch-ctx-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	switchWithRules := created.DeepCopy()
	switchWithRules.Context = rbacapi.ClusterContext
	_, err = extrbacapi.UpdateRoleTemplate(s.client, switchWithRules)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsBadRequest(err), "expected BadRequest(400), got %v", err)
	require.Contains(s.T(), err.Error(), "roletemplate.clusterScopedRules: Forbidden: ClusterScopedRules can only be set for project context RoleTemplates")

	stillProject, err := rbacapi.GetRoleTemplateByName(s.client, created.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), rbacapi.ProjectContext, stillProject.Context)
	require.Equal(s.T(), created.ClusterScopedRules, stillProject.ClusterScopedRules)

	cleared := stillProject.DeepCopy()
	cleared.ClusterScopedRules = []rbacv1.PolicyRule{}
	cleared, err = extrbacapi.UpdateRoleTemplate(s.client, cleared)
	require.NoError(s.T(), err)

	switchCleared := cleared.DeepCopy()
	switchCleared.Context = rbacapi.ClusterContext
	_, err = extrbacapi.UpdateRoleTemplate(s.client, switchCleared)
	require.NoError(s.T(), err)

	switched, err := rbacapi.GetRoleTemplateByName(s.client, created.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), rbacapi.ClusterContext, switched.Context)
	require.Empty(s.T(), switched.ClusterScopedRules)
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestAccessSurvivesRedundantBindingDeletion() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a user bound by two PRTBs with overlapping clusterScopedRules retains access after one binding is deleted")

	project, _, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	firstRT, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("overlap-rt-1")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	secondRT, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("overlap-rt-2")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	firstBinding, err := rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, firstRT.Name)
	require.NoError(s.T(), err)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, secondRT.Name)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, firstRT.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)
	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, secondRT.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	err = extrbacapi.DeleteProjectRoleTemplateBinding(s.client, firstBinding.Namespace, firstBinding.Name, true)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, firstRT.Name, user.Name, nil)
	require.NoError(s.T(), err)
	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, secondRT.Name, user.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	_, err = rbacapi.VerifyExactClusterRoleRules(s.client, s.cluster.ID, secondRT.Name+"-cluster-scoped", rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestRevokeInheritedClusterScopedRulesByClearingLeaf() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clearing clusterScopedRules on the leaf of an A -> B -> C chain revokes inherited pod access while preserving project access")

	leaf, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("revoke-leaf-c")},
		Context:            rbacapi.ProjectContext,
		Rules:              rbacapi.PolicyRules["readConfigMaps"],
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	middle, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{leaf}, false, false, nil)
	require.NoError(s.T(), err)

	root, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{middle}, false, false, nil)
	require.NoError(s.T(), err)

	project, namespace, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, root.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))

	cleared := leaf.DeepCopy()
	cleared.ClusterScopedRules = []rbacv1.PolicyRule{}
	_, err = extrbacapi.UpdateRoleTemplate(s.client, cleared)
	require.NoError(s.T(), err)

	err = kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		return rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false) == nil, nil
	})
	require.NoError(s.T(), err, "user still lists pods after clusterScopedRules were cleared on the leaf: access not revoked")

	require.NoError(s.T(), rbacapi.WaitForClusterRoleExistence(s.client, s.cluster.ID, leaf.Name+"-cluster-scoped", false), "leaf cluster-scoped ClusterRole still exists: ClusterRole still has the rule")

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
}

// Skipped Tests - GH#58349
func (s *ClusterScopedRulesLifecycleTestSuite) TestInheritedClusterScopedRulesAddedAfterBinding() {
	s.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58349")

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that adding clusterScopedRules to an inherited RoleTemplate grants cluster wide access")

	roletemplate := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("late-child")},
		Context:    rbacapi.ProjectContext,
		Rules:      rbacapi.PolicyRules["readConfigMaps"],
	}
	child, err := extrbacapi.CreateRoleTemplate(s.client, roletemplate)
	require.NoError(s.T(), err)

	parent, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{child}, false, false, nil)
	require.NoError(s.T(), err)

	project, namespace, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, parent.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, "", namespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, "", namespaceapi.KubeSystemNamespace, false, false))

	child.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.UpdateRoleTemplate(s.client, child)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, "", namespaceapi.KubeSystemNamespace, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, "", namespace.Name, true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestMultiLevelInheritanceAfterBinding() {
	s.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58349")

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules added to a leaf RoleTemplate after binding propagate through a three-level inheritance chain (A -> B -> C)")

	leaf, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("late-chain-c")},
		Context:    rbacapi.ProjectContext,
		Rules:      rbacapi.PolicyRules["readConfigMaps"],
	})
	require.NoError(s.T(), err)

	middle, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{leaf}, false, false, nil)
	require.NoError(s.T(), err)

	root, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{middle}, false, false, nil)
	require.NoError(s.T(), err)

	project, namespace, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, root.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.ConfigMapResource, "", namespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, "", namespaceapi.KubeSystemNamespace, false, false))

	leaf.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.UpdateRoleTemplate(s.client, leaf)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, "", namespaceapi.KubeSystemNamespace, true, false))
}

func (s *ClusterScopedRulesLifecycleTestSuite) TestRevokeInheritedClusterScopedRulesByUnlinkingChild() {
	s.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58349")

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that unlinking the leaf from the parent's roleTemplateNames revokes inherited pod access and re-linking restores it")

	leaf, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("unlink-leaf-c")},
		Context:            rbacapi.ProjectContext,
		Rules:              rbacapi.PolicyRules["readConfigMaps"],
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	middle, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{leaf}, false, false, nil)
	require.NoError(s.T(), err)

	root, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{middle}, false, false, nil)
	require.NoError(s.T(), err)

	project, _, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, root.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	unlinked := middle.DeepCopy()
	unlinked.RoleTemplateNames = []string{}
	_, err = extrbacapi.UpdateRoleTemplate(s.client, unlinked)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false)
	require.NoError(s.T(), err, "user still lists pods after the leaf was unlinked from the parent: access not revoked")

	stillLeaf, err := rbacapi.GetRoleTemplateByName(s.client, leaf.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), rbacapi.PolicyRules["readPods"], stillLeaf.ClusterScopedRules, "leaf clusterScopedRules must be unchanged after unlinking")

	relink, err := rbacapi.GetRoleTemplateByName(s.client, middle.Name)
	require.NoError(s.T(), err)
	relink.RoleTemplateNames = []string{leaf.Name}
	_, err = extrbacapi.UpdateRoleTemplate(s.client, relink)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
}

func TestClusterScopedRulesLifecycleTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterScopedRulesLifecycleTestSuite))
}

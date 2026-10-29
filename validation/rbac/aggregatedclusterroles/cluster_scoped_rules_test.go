//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package aggregatedclusterroles

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extconfigmapapi "github.com/rancher/shepherd/extensions/kubeapi/configmaps"
	extfeaturesapi "github.com/rancher/shepherd/extensions/kubeapi/features"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type ClusterScopedRulesTestSuite struct {
	suite.Suite
	client                *rancher.Client
	session               *session.Session
	cluster               *management.Cluster
	featureEnabledBySuite bool
}

func (s *ClusterScopedRulesTestSuite) SetupSuite() {
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

func (s *ClusterScopedRulesTestSuite) TearDownSuite() {
	s.session.Cleanup()

	if s.featureEnabledBySuite {
		log.Infof("Disabling the feature flag %s", rbacapi.AggregatedRoleTemplatesFeatureFlag)
		err := extfeaturesapi.DisableFeatureFlag(s.client, rbacapi.AggregatedRoleTemplatesFeatureFlag)
		if err != nil {
			log.Warnf("Failed to disable the feature flag during teardown: %v", err)
		}
	}
}

func (s *ClusterScopedRulesTestSuite) createTestResources(client *rancher.Client, clusterID string, rules, clusterScopedRules []rbacv1.PolicyRule) (*v3.Project, *corev1.Namespace, *v3.User, *rancher.Client, *v3.RoleTemplate, *v3.ProjectRoleTemplateBinding, error) {
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

func (s *ClusterScopedRulesTestSuite) TestClusterScopedRulesGrantClusterWideAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules grant cluster-wide pod access while namespaced rules stay project-scoped")

	_, namespace, user, userClient, roleTemplate, prtb, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	roleNames, bindingNames, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	err = rbacapi.VerifyBindingsForPrtb(s.client, s.cluster.ID, prtb, []*corev1.Namespace{namespace}, 1, len(bindingNames), roleNames...)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyMainACRContainsAllRules(s.client, s.cluster.ID, roleTemplate.Name, nil)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestClusterScopedRulesForNonNamespacedResource() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules grant access to non-namespaced node resources")

	rules := []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{rbacapi.NodesResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}}}
	_, namespace, user, userClient, roleTemplate, prtb, err := s.createTestResources(s.client, s.cluster.ID, nil, rules)
	require.NoError(s.T(), err)

	roleNames, bindingNames, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, rules)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyBindingsForPrtb(s.client, s.cluster.ID, prtb, []*corev1.Namespace{namespace}, 1, len(bindingNames), roleNames...)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.NodesResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.NodesResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.NodesResource, "", "", true, false))
}

func (s *ClusterScopedRulesTestSuite) TestPromotedAndExplicitClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that promoted and explicit cluster-scoped rules are both granted")

	nodes := rbacv1.PolicyRule{APIGroups: []string{""}, Resources: []string{rbacapi.NodesResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}}
	projectRules := append([]rbacv1.PolicyRule{nodes}, rbacapi.PolicyRules["readConfigMaps"]...)
	_, namespace, user, userClient, roleTemplate, binding, err := s.createTestResources(s.client, s.cluster.ID, projectRules, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	roleNames, bindingNames, err := rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, append([]rbacv1.PolicyRule{nodes}, rbacapi.PolicyRules["allPods"]...))
	require.NoError(s.T(), err)

	err = rbacapi.VerifyBindingsForPrtb(s.client, s.cluster.ID, binding, []*corev1.Namespace{namespace}, 1, len(bindingNames), roleNames...)
	require.NoError(s.T(), err)

	err = rbacapi.VerifyMainACRContainsAllRules(s.client, s.cluster.ID, roleTemplate.Name, nil)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true))
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.NodesResource, "", "", true, false))
}

func (s *ClusterScopedRulesTestSuite) TestPromotedRulesGrantClusterWidePermission() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that promoted rules in project RoleTemplate.Rules grant cluster-wide access")

	rules := []rbacv1.PolicyRule{
		{APIGroups: []string{""}, Resources: []string{rbacapi.PersistentVolumesResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}},
		{APIGroups: []string{rbacapi.StorageAPIGroup}, Resources: []string{rbacapi.StorageClassesResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}},
		{APIGroups: []string{rbacapi.APIRegistrationAPIGroup}, Resources: []string{rbacapi.APIServicesResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}},
		{APIGroups: []string{rbacapi.CatalogCattleAPIGroup}, Resources: []string{rbacapi.ClusterReposResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}},
		{APIGroups: []string{rbacapi.ManagementAPIGroup}, Resources: []string{rbacapi.ClustersResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.ListVerb}},
	}
	_, _, _, userClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, rules, nil)
	require.NoError(s.T(), err)

	for _, rule := range rules {
		resource := rule.Resources[0]
		apiGroup := rule.APIGroups[0]
		targetClusterID := s.cluster.ID
		if resource != rbacapi.ClustersResource && (apiGroup == rbacapi.ManagementAPIGroup || apiGroup == rbacapi.CatalogCattleAPIGroup) {
			targetClusterID = extclusterapi.LocalCluster
		}
		resourceName := ""
		if resource == rbacapi.ClustersResource {
			resourceName = extclusterapi.LocalCluster
		}
		for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
			require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, targetClusterID, verb, apiGroup, resource, "", resourceName, true))
		}
	}
}

func (s *ClusterScopedRulesTestSuite) TestMultipleUsersBoundToSameRoleTemplate() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that each bound user receives cluster-scoped access and an unrelated user does not")

	_, _, user1, userClient1, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	secondProject, err := projectapi.CreateProject(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	user2, userClient2, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user2.Name, secondProject, roleTemplate.Name)
	require.NoError(s.T(), err)

	controlProject, err := projectapi.CreateProject(s.client, s.cluster.ID)
	require.NoError(s.T(), err)
	controlUser, controlClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)
	controlRoleTemplate, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("scoped-control-rt")},
		Context:    rbacapi.ProjectContext,
		Rules:      rbacapi.PolicyRules["readConfigMaps"],
	})
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, controlUser.Name, controlProject, controlRoleTemplate.Name)
	require.NoError(s.T(), err)

	for _, userName := range []string{user1.Name, user2.Name} {
		_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, userName, rbacapi.PolicyRules["allPods"])
		require.NoError(s.T(), err)
	}

	for _, client := range []*rancher.Client{userClient1, userClient2} {
		for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
			require.NoError(s.T(), rbacapi.VerifyUserCanI(client, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
		}
	}

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, controlUser.Name, nil)
	require.NoError(s.T(), err, "control user should not have a ClusterRoleBinding for the all-pods RoleTemplate")
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(controlClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}

	for _, client := range []*rancher.Client{userClient1, userClient2} {
		require.NoError(s.T(), rbacapi.VerifyUserPermission(client, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(controlClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestRejectClusterContext() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules are rejected for cluster-context RoleTemplates")

	roleTemplate := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("scoped-rt"),
		},
		Context:            rbacapi.ClusterContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	}
	_, err := extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsBadRequest(err), "expected BadRequest(400), got %v", err)
	require.Contains(s.T(), err.Error(), `admission webhook "rancher.cattle.io.roletemplates.management.cattle.io" denied the request`)
	require.Contains(s.T(), err.Error(), "roletemplate.clusterScopedRules: Forbidden: ClusterScopedRules can only be set for project context RoleTemplates")

	_, err = rbacapi.GetRoleTemplateByName(s.client, roleTemplate.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "rejected RoleTemplate must not exist: %v", err)

	roleTemplate.Context = rbacapi.ProjectContext
	created, err := extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.NoError(s.T(), err)

	updated := created.DeepCopy()
	updated.Context = rbacapi.ClusterContext
	_, err = extrbacapi.UpdateRoleTemplate(s.client, updated)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsBadRequest(err), "expected BadRequest(400), got %v", err)
	require.Contains(s.T(), err.Error(), `admission webhook "rancher.cattle.io.roletemplates.management.cattle.io" denied the request`)
	require.Contains(s.T(), err.Error(), "roletemplate.clusterScopedRules: Forbidden: ClusterScopedRules can only be set for project context RoleTemplates")

	roleTemplateContext, err := rbacapi.GetRoleTemplateContext(s.client, created.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), rbacapi.ProjectContext, roleTemplateContext)

	unchanged, err := rbacapi.GetRoleTemplateByName(s.client, created.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), created.ClusterScopedRules, unchanged.ClusterScopedRules)
}

func (s *ClusterScopedRulesTestSuite) TestRejectMissingClusterScopedRuleVerbs() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules with missing verbs are rejected")

	roleTemplate := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("invalid-scoped-rt")},
		Context:    rbacapi.ProjectContext,
		ClusterScopedRules: []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Resources: []string{rbacapi.PodsResource},
		}},
	}
	_, err := extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsInvalid(err), "expected Invalid(422), got %v", err)
	require.Contains(s.T(), err.Error(), "clusterScopedRules[0].verbs: Required value")

	_, err = rbacapi.GetRoleTemplateByName(s.client, roleTemplate.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "rejected RoleTemplate must not exist: %v", err)

	roleTemplate.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.NoError(s.T(), err)
}

func (s *ClusterScopedRulesTestSuite) TestRejectMissingClusterScopedRuleResources() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules with missing resources are rejected")

	roleTemplate := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("invalid-scoped-rt")},
		Context:    rbacapi.ProjectContext,
		ClusterScopedRules: []rbacv1.PolicyRule{{
			APIGroups: []string{""},
			Verbs:     []string{rbacapi.GetVerb},
		}},
	}
	_, err := extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsBadRequest(err), "expected BadRequest(400), got %v", err)
	require.Contains(s.T(), err.Error(), "clusterScopedRules[0].resources: Required value")

	_, err = rbacapi.GetRoleTemplateByName(s.client, roleTemplate.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "rejected RoleTemplate must not exist: %v", err)

	roleTemplate.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.NoError(s.T(), err)
}

func (s *ClusterScopedRulesTestSuite) TestRejectMissingClusterScopedRuleAPIGroups() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules with missing apiGroups are rejected")

	roleTemplate := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("invalid-scoped-rt")},
		Context:    rbacapi.ProjectContext,
		ClusterScopedRules: []rbacv1.PolicyRule{{
			Resources: []string{rbacapi.PodsResource},
			Verbs:     []string{rbacapi.GetVerb},
		}},
	}
	_, err := extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsBadRequest(err), "expected BadRequest(400), got %v", err)
	require.Contains(s.T(), err.Error(), "clusterScopedRules[0].apiGroups: Required value")

	_, err = rbacapi.GetRoleTemplateByName(s.client, roleTemplate.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "rejected RoleTemplate must not exist: %v", err)

	roleTemplate.ClusterScopedRules = rbacapi.PolicyRules["readPods"]
	_, err = extrbacapi.CreateRoleTemplate(s.client, roleTemplate)
	require.NoError(s.T(), err)
}

func (s *ClusterScopedRulesTestSuite) TestBlockPrivilegeEscalation() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that users cannot author or bind RoleTemplates with permissions they do not hold")

	project, _, user, userClient, _, binding, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], nil)
	require.NoError(s.T(), err)

	err = rbacapi.GrantRoleTemplateAndPRTBAuthoring(s.client, user.Name)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.CreateVerb, rbacapi.GetVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, extclusterapi.LocalCluster, verb, rbacapi.ManagementAPIGroup, rbacapi.RoleTemplateResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, extclusterapi.LocalCluster, rbacapi.CreateVerb, rbacapi.ManagementAPIGroup, rbacapi.PrtbResource, binding.Namespace, "", true))

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))

	requested := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{Name: namegen.AppendRandomString("escalating-rt")},
		Context:    rbacapi.ProjectContext, ClusterScopedRules: rbacapi.PolicyRules["allPods"],
	}
	_, err = extrbacapi.CreateRoleTemplate(userClient, requested)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err), "expected admission denial, got %v", err)

	_, err = rbacapi.GetRoleTemplateByName(s.client, requested.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "rejected RoleTemplate must not exist: %v", err)

	created, err := extrbacapi.CreateRoleTemplate(s.client, requested)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, project, created.Name)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err))
	require.Contains(s.T(), err.Error(), "attempting to grant RBAC permissions not currently held")

	bindings, err := s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List("", metav1.ListOptions{})
	require.NoError(s.T(), err)
	for _, rejectedBinding := range bindings.Items {
		require.False(s.T(), rejectedBinding.UserName == user.Name && rejectedBinding.RoleTemplateName == created.Name && rejectedBinding.ProjectName == project.Namespace+":"+project.Name, "rejected PRTB %s persisted", rejectedBinding.Name)
	}
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, user.Name, project, created.Name)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, created.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", true))
}

func (s *ClusterScopedRulesTestSuite) TestPermissionsHeldThroughCrtb() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that CRTB-held permissions allow project bindings for pods but not unheld secrets permissions")

	project, _, user, userClient, _, prtb, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), rbacapi.GrantRoleTemplateAndPRTBAuthoring(s.client, user.Name))

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, extclusterapi.LocalCluster, rbacapi.CreateVerb, rbacapi.ManagementAPIGroup, rbacapi.PrtbResource, prtb.Namespace, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))

	clusterRole, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ClusterContext, rbacapi.PolicyRules["allPods"], nil, false, false, nil)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateClusterRoleTemplateBinding(s.client, s.cluster.ID, user.Name, clusterRole.Name)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.SecretsResource, "", "", false))

	requested := &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("held-crtb-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["allPods"],
	}
	created, err := extrbacapi.CreateRoleTemplate(s.client, requested)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, project, created.Name)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, created.Name, user.Name, rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	requested.Name = namegen.AppendRandomString("unheld-secrets-rt")
	requested.ClusterScopedRules = []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{rbacapi.SecretsResource}, Verbs: []string{rbacapi.Wildcard}}}
	secretsRoleTemplate, err := extrbacapi.CreateRoleTemplate(s.client, requested)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, project, secretsRoleTemplate.Name)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err), "expected admission denial, got %v", err)
	require.Contains(s.T(), err.Error(), "attempting to grant RBAC permissions not currently held")

	prtbs, err := s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List(prtb.Namespace, metav1.ListOptions{})
	require.NoError(s.T(), err)
	for _, prtb := range prtbs.Items {
		require.False(s.T(), prtb.UserName == user.Name && prtb.RoleTemplateName == secretsRoleTemplate.Name && prtb.ProjectName == prtb.ProjectName, "rejected PRTB %s persisted", prtb.Name)
	}
	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, secretsRoleTemplate.Name, user.Name, nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.SecretsResource, "", "", false))
}

func (s *ClusterScopedRulesTestSuite) TestReusePermissionsAcrossProjects() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a user can reuse held permissions in another project but cannot request broader permissions")

	_, _, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)

	err = rbacapi.GrantRoleTemplateAndPRTBAuthoring(s.client, user.Name)
	require.NoError(s.T(), err)

	secondProject, err := projectapi.CreateProject(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	bindingNamespace := rbacapi.GetProjectRoleTemplateBindingNamespace(secondProject)
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, extclusterapi.LocalCluster, rbacapi.CreateVerb, rbacapi.ManagementAPIGroup, rbacapi.PrtbResource, bindingNamespace, "", true))
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))

	secondBinding, err := rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, secondProject, roleTemplate.Name)
	require.NoError(s.T(), err)

	persistedBinding, err := s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Get(secondBinding.Namespace, secondBinding.Name, metav1.GetOptions{})
	require.NoError(s.T(), err)
	require.Equal(s.T(), user.Name, persistedBinding.UserName)
	require.Equal(s.T(), roleTemplate.Name, persistedBinding.RoleTemplateName)
	require.Equal(s.T(), secondBinding.ProjectName, persistedBinding.ProjectName)

	broaderRT := &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("broader-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["writePods"]}
	broader, err := extrbacapi.CreateRoleTemplate(s.client, broaderRT)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, secondProject, broader.Name)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err), "expected admission denial, got %v", err)
	require.Contains(s.T(), err.Error(), "attempting to grant RBAC permissions not currently held")

	bindings, err := s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List("", metav1.ListOptions{})
	require.NoError(s.T(), err)

	for _, rejectedBinding := range bindings.Items {
		require.False(s.T(), rejectedBinding.UserName == user.Name && rejectedBinding.RoleTemplateName == broader.Name && rejectedBinding.ProjectName == secondBinding.ProjectName, "rejected PRTB %s persisted", rejectedBinding.Name)
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.PodsResource, "", "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))
}

func (s *ClusterScopedRulesTestSuite) TestInheritedPermissionsSatisfyEscalationChecks() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that inherited permissions satisfy escalation checks but cannot be exceeded")

	roletemplate := &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("inherited-scoped-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	}
	childRT, err := extrbacapi.CreateRoleTemplate(s.client, roletemplate)
	require.NoError(s.T(), err)

	project, _, user, userClient, mainRT, binding, err := s.createTestResources(s.client, s.cluster.ID, nil, nil)
	require.NoError(s.T(), err)

	updatedMainRT, err := rbacapi.UpdateRoleTemplateInheritance(s.client, mainRT.Name, []*v3.RoleTemplate{childRT})
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, updatedMainRT.Name, user.Name, childRT.ClusterScopedRules)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))
	require.NoError(s.T(), rbacapi.GrantRoleTemplateAndPRTBAuthoring(s.client, user.Name))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, extclusterapi.LocalCluster, rbacapi.CreateVerb, rbacapi.ManagementAPIGroup, rbacapi.PrtbResource, binding.Namespace, "", true))

	inheritedRoleTemplate, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{childRT}, false, false, nil)
	require.NoError(s.T(), err)

	acceptedBinding, err := rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, project, inheritedRoleTemplate.Name)
	require.NoError(s.T(), err)
	_, err = s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Get(acceptedBinding.Namespace, acceptedBinding.Name, metav1.GetOptions{})
	require.NoError(s.T(), err)
	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, inheritedRoleTemplate.Name, user.Name, childRT.ClusterScopedRules)
	require.NoError(s.T(), err)

	requested := &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("broader-child-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["writePods"],
	}
	broaderChildRT, err := extrbacapi.CreateRoleTemplate(s.client, requested)
	require.NoError(s.T(), err)

	broader, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ProjectContext, nil, []*v3.RoleTemplate{broaderChildRT}, false, false, nil)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(userClient, user.Name, project, broader.Name)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err), "expected admission denial, got %v", err)
	require.Contains(s.T(), err.Error(), "attempting to grant RBAC permissions not currently held")

	prtbs, err := s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List(binding.Namespace, metav1.ListOptions{})
	require.NoError(s.T(), err)
	for _, rejectedBinding := range prtbs.Items {
		require.False(s.T(), rejectedBinding.UserName == user.Name && rejectedBinding.RoleTemplateName == broader.Name && rejectedBinding.ProjectName == binding.ProjectName, "rejected PRTB %s persisted", rejectedBinding.Name)
	}
	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, broader.Name, user.Name, nil)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))
}

func (s *ClusterScopedRulesTestSuite) TestEmptyClusterScopedRulesGrantNoAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that empty clusterScopedRules grant no access")

	_, namespace, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], nil)
	require.NoError(s.T(), err)

	_, err = s.client.WranglerContext.Mgmt.RoleTemplate().Patch(roleTemplate.Name, types.MergePatchType, []byte(`{"clusterScopedRules":[]}`))
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))

	err = rbacapi.VerifyRoleTemplateResourceRules(s.client, s.cluster.ID, roleTemplate.Name, "", rbacapi.PodsResource, false)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, nil)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestOmittedClusterScopedRulesGrantNoAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that omitted clusterScopedRules grant no access")

	_, namespace, user, userClient, roleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], nil)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespace.Name, "", true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false))

	err = rbacapi.VerifyRoleTemplateResourceRules(s.client, s.cluster.ID, roleTemplate.Name, "", rbacapi.PodsResource, false)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, nil)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.CreateVerb, rbacapi.DeleteVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestWildcardClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that wildcard clusterScopedRules grant broad access and cannot be escalated by another user")

	wildcard := []rbacv1.PolicyRule{{APIGroups: []string{rbacapi.Wildcard}, Resources: []string{rbacapi.Wildcard}, Verbs: []string{rbacapi.Wildcard}}}
	_, _, user, userClient, roleTemplate, binding, err := s.createTestResources(s.client, s.cluster.ID, nil, wildcard)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, roleTemplate.Name, user.Name, wildcard)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, "", "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.NodesResource, "", "", true))

	user2, user2Client, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	err = rbacapi.GrantRoleTemplateAndPRTBAuthoring(s.client, user2.Name)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.CreateVerb, rbacapi.GetVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(user2Client, extclusterapi.LocalCluster, verb, rbacapi.ManagementAPIGroup, rbacapi.RoleTemplateResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(user2Client, extclusterapi.LocalCluster, rbacapi.CreateVerb, rbacapi.ManagementAPIGroup, rbacapi.PrtbResource, binding.Namespace, "", true))

	requested := &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("unauthorized-wildcard-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: wildcard,
	}
	_, err = extrbacapi.CreateRoleTemplate(user2Client, requested)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err), "expected Forbidden error, got %v", err)

	_, err = rbacapi.GetRoleTemplateByName(s.client, requested.Name)
	require.True(s.T(), apierrors.IsNotFound(err), "rejected RoleTemplate must not exist: %v", err)
}

func (s *ClusterScopedRulesTestSuite) TestNamespaceRulesVersusExplicitRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that project namespace access stays scoped while explicit cluster rules apply across namespaces")

	namespaceRules := []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{"namespaces"}, Verbs: []string{rbacapi.GetVerb}}}
	_, projectNamespace, projectUser, projectUserClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, namespaceRules, nil)
	require.NoError(s.T(), err)
	_, clusterNamespace, clusterUser, clusterUserClient, clusterRoleTemplate, _, err := s.createTestResources(s.client, s.cluster.ID, nil, namespaceRules)
	require.NoError(s.T(), err)

	unrelated, err := extnamespaceapi.CreateNamespace(s.client, s.cluster.ID, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "unrelated-scoped-"}})
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(projectUserClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.NamespaceResource, "", projectNamespace.Name, true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(projectUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", projectNamespace.Name, true, false))

	for _, name := range []string{clusterNamespace.Name, unrelated.Name} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(projectUserClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.NamespaceResource, "", name, false))
		require.NoError(s.T(), rbacapi.VerifyUserPermission(projectUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", name, false, false))
	}

	require.NoError(s.T(), rbacapi.VerifyUserCanI(projectUserClient, s.cluster.ID, rbacapi.CreateVerb, "", rbacapi.NamespaceResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(projectUserClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.NamespaceResource, "", projectNamespace.Name, false))

	for _, name := range []string{projectNamespace.Name, clusterNamespace.Name, unrelated.Name} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(clusterUserClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.NamespaceResource, "", name, true))
		require.NoError(s.T(), rbacapi.VerifyUserPermission(clusterUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", name, true, false))
		for _, verb := range []string{rbacapi.DeleteVerb, rbacapi.UpdateVerb} {
			require.NoError(s.T(), rbacapi.VerifyUserCanI(clusterUserClient, s.cluster.ID, verb, "", rbacapi.NamespaceResource, "", name, false))
		}
	}

	for _, verb := range []string{rbacapi.ListVerb, rbacapi.CreateVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(clusterUserClient, s.cluster.ID, verb, "", rbacapi.NamespaceResource, "", "", false))
	}

	err = rbacapi.VerifyUserNamespaceRuleRestriction(s.client, s.cluster.ID, projectUser.Name, projectNamespace.Name, unrelated.Name)
	require.NoError(s.T(), err, "project namespace RBAC should stay restricted to project namespaces")

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, clusterRoleTemplate.Name, clusterUser.Name, namespaceRules)
	require.NoError(s.T(), err)
}

func (s *ClusterScopedRulesTestSuite) TestEscalationDeniedForBoundRoleTemplateUpdate() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a user with limited permissions cannot update an existing, bound RoleTemplate to add clusterScopedRules they do not hold")

	_, _, author, authorClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, nil, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)
	require.NoError(s.T(), rbacapi.GrantRoleTemplateAndPRTBAuthoring(s.client, author.Name))

	require.NoError(s.T(), rbacapi.VerifyUserCanI(authorClient, extclusterapi.LocalCluster, rbacapi.UpdateVerb, rbacapi.ManagementAPIGroup, rbacapi.RoleTemplateResource, "", "", true))
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(authorClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(authorClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))

	targetProject, _, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)
	targetUser, targetClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)
	boundRT, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("bound-scoped-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, targetUser.Name, targetProject, boundRT.Name)
	require.NoError(s.T(), err)

	_, _, err = rbacapi.VerifyClusterScopedRoleTemplateRBAC(s.client, s.cluster.ID, boundRT.Name, targetUser.Name, rbacapi.PolicyRules["readPods"])
	require.NoError(s.T(), err)
	require.NoError(s.T(), rbacapi.VerifyUserCanI(targetClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))

	escalated, err := rbacapi.GetRoleTemplateByName(s.client, boundRT.Name)
	require.NoError(s.T(), err)
	escalated.ClusterScopedRules = rbacapi.PolicyRules["writePods"]
	_, err = extrbacapi.UpdateRoleTemplate(authorClient, escalated)
	require.Error(s.T(), err)
	require.True(s.T(), apierrors.IsForbidden(err), "expected admission denial on update, got %v", err)

	unchanged, err := rbacapi.GetRoleTemplateByName(s.client, boundRT.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), rbacapi.PolicyRules["readPods"], unchanged.ClusterScopedRules)
	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(targetClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", true))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(targetClient, s.cluster.ID, rbacapi.DeleteVerb, "", rbacapi.PodsResource, "", "", false))
}

func (s *ClusterScopedRulesTestSuite) TestProjectScopedPodAccessDeniesClusterScopedPods() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that project-scoped pod rules grant access to pods only in the project namespace and deny cluster-scoped pod access")

	_, namespace, _, userClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readPods"], nil)
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, namespace.Name, "", true))
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, "", "", false))
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestCRTBDoesNotGrantInheritedClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a cluster-context RoleTemplate inheriting a project RoleTemplate's clusterScopedRules does not grant cluster-wide access through a CRTB")

	projRT, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("crtb-proj-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	controlProject, _, err := projectapi.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	controlUser, controlClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateProjectRoleTemplateBinding(s.client, controlUser.Name, controlProject, projRT.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(controlClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	clusterRT, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ClusterContext, nil, []*v3.RoleTemplate{projRT}, false, false, nil)
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateClusterRoleTemplateBinding(s.client, s.cluster.ID, user.Name, clusterRT.Name)
	require.NoError(s.T(), err)

	err = rbacapi.WaitForClusterRoleExistence(s.client, s.cluster.ID, clusterRT.Name+"-cluster-scoped-aggregator", false)
	require.NoError(s.T(), err, "cluster-scoped aggregator ClusterRole must not be generated for a cluster-context RoleTemplate")

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestGlobalRoleDoesNotGrantInheritedClusterScopedRules() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules inherited by a GlobalRole through inheritedClusterRoles do not grant cluster-wide access on a downstream cluster")

	projRT, err := extrbacapi.CreateRoleTemplate(s.client, &v3.RoleTemplate{
		ObjectMeta:         metav1.ObjectMeta{Name: namegen.AppendRandomString("gr-proj-rt")},
		Context:            rbacapi.ProjectContext,
		ClusterScopedRules: rbacapi.PolicyRules["readPods"],
	})
	require.NoError(s.T(), err)

	clusterRT, err := rbacapi.CreateRoleTemplate(s.client, rbacapi.ClusterContext, nil, []*v3.RoleTemplate{projRT}, false, false, nil)
	require.NoError(s.T(), err)

	globalRole, err := rbacapi.CreateGlobalRoleWithInheritedClusterRoles(s.client, []string{clusterRT.Name})
	require.NoError(s.T(), err)

	user, userClient, err := userapi.CreateUserWithRolesAndClient(s.client, rbac.StandardUser.String())
	require.NoError(s.T(), err)
	_, err = rbacapi.CreateGlobalRoleBinding(s.client, globalRole.Name, user.Name, "", "")
	require.NoError(s.T(), err)

	for _, verb := range []string{rbacapi.GetVerb, rbacapi.ListVerb} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, verb, "", rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestResourceNamesLimitClusterScopedAccess() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that resourceNames on clusterScopedRules restrict cluster-wide access to only the named objects")

	allowedCM := namegen.AppendRandomString("allowed-cm")
	deniedCM := namegen.AppendRandomString("denied-cm")
	configMapRules := []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{rbacapi.ConfigMapResource}, Verbs: []string{rbacapi.GetVerb}, ResourceNames: []string{allowedCM}}}
	_, namespace, _, userClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, nil, configMapRules)
	require.NoError(s.T(), err)

	for _, ns := range []string{namespace.Name, namespaceapi.KubeSystemNamespace} {
		_, err = extconfigmapapi.CreateConfigMapWithTemplate(s.client, s.cluster.ID, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: allowedCM, Namespace: ns}})
		require.NoError(s.T(), err)
		_, err = extconfigmapapi.CreateConfigMapWithTemplate(s.client, s.cluster.ID, &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: deniedCM, Namespace: ns}})
		require.NoError(s.T(), err)
	}

	for _, ns := range []string{namespace.Name, namespaceapi.KubeSystemNamespace} {
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, ns, allowedCM, true))
		require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.ConfigMapResource, ns, deniedCM, false))
		require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.ConfigMapResource, ns, allowedCM, true, false))
		require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.ConfigMapResource, ns, deniedCM, false, false))
	}
	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.ConfigMapResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespace.Name, "", false, false))

	nsAllowed, err := extnamespaceapi.CreateNamespace(s.client, s.cluster.ID, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "rn-allowed-"}})
	require.NoError(s.T(), err)
	nsDenied, err := extnamespaceapi.CreateNamespace(s.client, s.cluster.ID, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "rn-denied-"}})
	require.NoError(s.T(), err)

	namespaceRules := []rbacv1.PolicyRule{{APIGroups: []string{""}, Resources: []string{rbacapi.NamespaceResource}, Verbs: []string{rbacapi.GetVerb}, ResourceNames: []string{nsAllowed.Name}}}
	_, _, _, nsUserClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, nil, namespaceRules)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(nsUserClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.NamespaceResource, "", nsAllowed.Name, true))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(nsUserClient, s.cluster.ID, rbacapi.GetVerb, "", rbacapi.NamespaceResource, "", nsDenied.Name, false))
	require.NoError(s.T(), rbacapi.VerifyUserCanI(nsUserClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.NamespaceResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(nsUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", nsAllowed.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(nsUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", nsDenied.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(nsUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.NamespaceResource, "", "", false, false))
}

func (s *ClusterScopedRulesTestSuite) TestClusterScopedRulesClusterIsolation() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that clusterScopedRules grant pod access only on the project's cluster and not on the local cluster")

	_, _, _, userClient, _, _, err := s.createTestResources(s.client, s.cluster.ID, rbacapi.PolicyRules["readConfigMaps"], rbacapi.PolicyRules["allPods"])
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, s.cluster.ID, rbacapi.ListVerb, "", rbacapi.PodsResource, "", "", true))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", true, false))

	require.NoError(s.T(), rbacapi.VerifyUserCanI(userClient, extclusterapi.LocalCluster, rbacapi.ListVerb, "", rbacapi.PodsResource, "", "", false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(userClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.PodsResource, namespaceapi.KubeSystemNamespace, "", false, false))
}

func TestClusterScopedRulesTestSuite(t *testing.T) {
	suite.Run(t, new(ClusterScopedRulesTestSuite))
}

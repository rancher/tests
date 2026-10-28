//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14

package inheritednamespacedrules

import (
	"encoding/json"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
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
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

type InheritedNamespacedRulesEscalationTestSuite struct {
	suite.Suite
	session *session.Session
	client  *rancher.Client
	cluster *management.Cluster
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TearDownSuite() {
	inr.session.Cleanup()
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) SetupSuite() {
	inr.session = session.NewSession()

	client, err := rancher.NewClient("", inr.session)
	require.NoError(inr.T(), err)
	inr.client = client

	require.NotEmpty(inr.T(), client.RancherConfig.ClusterName, "A downstream cluster must be configured")
	clusterID, err := clusters.GetClusterIDByName(client, client.RancherConfig.ClusterName)
	require.NoError(inr.T(), err)
	require.NotEqual(inr.T(), extclusterapi.LocalCluster, clusterID, "The configured cluster must be downstream")
	inr.cluster, err = client.Management.Cluster.ByID(clusterID)
	require.NoError(inr.T(), err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) setupEscalation(apiGroup, resource string, verbs []string) (*rancher.Client, *v3.User, string, string, map[string][]rbacv1.PolicyRule, *v3.GlobalRole) {
	project, err := projectapi.CreateProject(inr.client, inr.cluster.ID)
	require.NoError(inr.T(), err)

	sharedNamespace := namegen.AppendRandomString("shared-namespace-")
	otherNamespace := namegen.AppendRandomString("other-namespace-")
	_, err = namespaceapi.CreateNamespace(inr.client, inr.cluster.ID, project.Name, sharedNamespace, "", nil, nil)
	require.NoError(inr.T(), err, "Failed to create downstream namespace %s", sharedNamespace)
	_, err = namespaceapi.CreateNamespace(inr.client, inr.cluster.ID, project.Name, otherNamespace, "", nil, nil)
	require.NoError(inr.T(), err, "Failed to create downstream namespace %s", otherNamespace)

	user1, password, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err)
	user2, _, err := userapi.CreateUserWithRoles(inr.client, rbac.StandardUser.String())
	require.NoError(inr.T(), err)
	_, err = rbacapi.CreateClusterRoleTemplateBinding(inr.client, extclusterapi.LocalCluster, user1.Name, rbac.ClusterMember.String())
	require.NoError(inr.T(), err)

	limitedRules := map[string][]rbacv1.PolicyRule{
		sharedNamespace: {
			{
				APIGroups: []string{apiGroup},
				Resources: []string{resource},
				Verbs:     verbs,
			},
		},
	}
	delegatedRole, err := extrbacapi.CreateGlobalRole(inr.client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("delegated-gr-"),
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{rbacapi.ManagementAPIGroup},
				Resources: []string{rbacapi.GlobalRoleResource, rbacapi.GlobalRoleBindingResource},
				Verbs:     []string{rbacapi.GetVerb, rbacapi.ListVerb, rbacapi.WatchVerb, rbacapi.CreateVerb, rbacapi.UpdateVerb, rbacapi.DeleteVerb, rbacapi.PatchVerb},
			},
		},
		InheritedNamespacedRules: limitedRules,
	})
	require.NoError(inr.T(), err)
	_, err = rbacapi.CreateGlobalRoleBinding(inr.client, delegatedRole.Name, user1.Name, "", "")
	require.NoError(inr.T(), err)

	adminOnlyRole, err := extrbacapi.CreateGlobalRole(inr.client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("admin-only-gr-"),
		},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {
				{
					APIGroups: []string{rbacapi.Wildcard},
					Resources: []string{rbacapi.Wildcard},
					Verbs:     []string{rbacapi.Wildcard},
				},
			},
		},
	})
	require.NoError(inr.T(), err)

	user1Client, err := inr.client.AsPublicAPIUser(user1, password)
	require.NoError(inr.T(), err)
	user1Client, err = user1Client.ReLogin()
	require.NoError(inr.T(), err)
	_, err = rbacapi.GetGlobalRoleByName(user1Client, delegatedRole.Name)
	require.NoError(inr.T(), err)

	return user1Client, user2, sharedNamespace, otherNamespace, limitedRules, adminOnlyRole
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateWithBroaderResourcesAndVerbsReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with only get/list permissions cannot create inherited namespaced rules with broader resources and verbs")

	user1Client, _, sharedNamespace, _, _, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	roleName := namegen.AppendRandomString("t1-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.SecretsResource},
				Verbs:     []string{rbacapi.Wildcard},
			}},
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateWithBroaderResourcesAndVerbsEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with edit permission cannot create inherited namespaced rules with broader resources and verbs")

	user1Client, _, sharedNamespace, _, _, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.EditVerb})

	roleName := namegen.AppendRandomString("t1-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.SecretsResource},
				Verbs:     []string{rbacapi.Wildcard},
			}},
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateWithWildcardAPIGroupReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with apps get/list permissions cannot use a wildcard API group to broaden access")

	user1Client, _, sharedNamespace, _, _, _ := inr.setupEscalation(rbacapi.AppsAPIGroup, rbacapi.AppsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	roleName := namegen.AppendRandomString("t2-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.AppsResource},
				Verbs:     []string{rbacapi.GetVerb, rbacapi.ListVerb},
			}},
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateWithWildcardAPIGroupEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with apps edit permission cannot use a wildcard API group to broaden access")

	user1Client, _, sharedNamespace, _, _, _ := inr.setupEscalation(rbacapi.AppsAPIGroup, rbacapi.AppsResource, []string{rbacapi.EditVerb})

	roleName := namegen.AppendRandomString("t2-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.AppsResource},
				Verbs:     []string{rbacapi.EditVerb},
			}},
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateWithWildcardAPIGroupForAppsReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with apps/deployments get/list permissions cannot use a wildcard API group to broaden access")

	user1Client, _, sharedNamespace, _, _, _ := inr.setupEscalation(rbacapi.AppsAPIGroup, rbacapi.DeploymentsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	roleName := namegen.AppendRandomString("t2-apps-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.DeploymentsResource},
				Verbs:     []string{rbacapi.GetVerb, rbacapi.ListVerb},
			}},
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateWithWildcardAPIGroupForAppsEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with apps/deployments edit permission cannot use a wildcard API group to broaden access")

	user1Client, _, sharedNamespace, _, _, _ := inr.setupEscalation(rbacapi.AppsAPIGroup, rbacapi.DeploymentsResource, []string{rbacapi.EditVerb})

	roleName := namegen.AppendRandomString("t2-apps-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.DeploymentsResource},
				Verbs:     []string{rbacapi.EditVerb},
			}},
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotPatchWithBroaderPermissionsReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with only get/list permissions cannot patch a GlobalRole to grant broader inherited namespaced permissions")

	user1Client, _, sharedNamespace, _, limitedRules, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	roleName := namegen.AppendRandomString("t3-gr-")
	originalRole, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta:               metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: limitedRules,
	})
	require.NoError(inr.T(), err, "Creating a role with permissions already held should succeed")

	patch, err := json.Marshal(struct {
		InheritedNamespacedRules map[string][]rbacv1.PolicyRule `json:"inheritedNamespacedRules"`
	}{
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.SecretsResource},
				Verbs:     []string{rbacapi.Wildcard},
			}},
		},
	})
	require.NoError(inr.T(), err)
	_, err = user1Client.WranglerContext.Mgmt.GlobalRole().Patch(roleName, types.MergePatchType, patch)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	persistedRole, err := rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.NoError(inr.T(), err)
	require.Equal(inr.T(), originalRole.InheritedNamespacedRules, persistedRole.InheritedNamespacedRules,
		"A rejected patch must leave the original inherited namespaced rules unchanged")
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotPatchWithBroaderPermissionsEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with edit permission cannot patch a GlobalRole to grant broader inherited namespaced permissions")

	user1Client, _, sharedNamespace, _, limitedRules, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.EditVerb})

	roleName := namegen.AppendRandomString("t3-gr-")
	originalRole, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta:               metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: limitedRules,
	})
	require.NoError(inr.T(), err, "Creating a role with permissions already held should succeed")

	patch, err := json.Marshal(struct {
		InheritedNamespacedRules map[string][]rbacv1.PolicyRule `json:"inheritedNamespacedRules"`
	}{
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			sharedNamespace: {{
				APIGroups: []string{rbacapi.Wildcard},
				Resources: []string{rbacapi.SecretsResource},
				Verbs:     []string{rbacapi.Wildcard},
			}},
		},
	})
	require.NoError(inr.T(), err)
	_, err = user1Client.WranglerContext.Mgmt.GlobalRole().Patch(roleName, types.MergePatchType, patch)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	persistedRole, err := rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.NoError(inr.T(), err)
	require.Equal(inr.T(), originalRole.InheritedNamespacedRules, persistedRole.InheritedNamespacedRules,
		"A rejected patch must leave the original inherited namespaced rules unchanged")
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotUpdateWithBroaderPermissionsReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with only get/list permissions cannot update a GlobalRole to grant broader inherited namespaced permissions")

	user1Client, _, sharedNamespace, _, limitedRules, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	roleName := namegen.AppendRandomString("t3-update-gr-")
	originalRole, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta:               metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: limitedRules,
	})
	require.NoError(inr.T(), err, "Creating a role with permissions already held should succeed")

	updatedRole, err := rbacapi.GetGlobalRoleByName(user1Client, roleName)
	require.NoError(inr.T(), err, "Fetching the latest role before update should succeed")
	updatedRole.InheritedNamespacedRules[sharedNamespace] = append(
		updatedRole.InheritedNamespacedRules[sharedNamespace],
		rbacv1.PolicyRule{
			APIGroups: []string{rbacapi.Wildcard},
			Resources: []string{rbacapi.SecretsResource},
			Verbs:     []string{rbacapi.Wildcard},
		},
	)
	_, err = user1Client.WranglerContext.Mgmt.GlobalRole().Update(updatedRole)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	persistedRole, err := rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.NoError(inr.T(), err)
	require.Equal(inr.T(), originalRole.InheritedNamespacedRules, persistedRole.InheritedNamespacedRules,
		"A rejected update must leave the original inherited namespaced rules unchanged")
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotUpdateWithBroaderPermissionsEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with edit permission cannot update a GlobalRole to grant broader inherited namespaced permissions")

	user1Client, _, sharedNamespace, _, limitedRules, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.EditVerb})

	roleName := namegen.AppendRandomString("t3-update-gr-")
	originalRole, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta:               metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: limitedRules,
	})
	require.NoError(inr.T(), err, "Creating a role with permissions already held should succeed")

	updatedRole, err := rbacapi.GetGlobalRoleByName(user1Client, roleName)
	require.NoError(inr.T(), err, "Fetching the latest role before update should succeed")
	updatedRole.InheritedNamespacedRules[sharedNamespace] = append(
		updatedRole.InheritedNamespacedRules[sharedNamespace],
		rbacv1.PolicyRule{
			APIGroups: []string{rbacapi.Wildcard},
			Resources: []string{rbacapi.SecretsResource},
			Verbs:     []string{rbacapi.Wildcard},
		},
	)
	_, err = user1Client.WranglerContext.Mgmt.GlobalRole().Update(updatedRole)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	persistedRole, err := rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.NoError(inr.T(), err)
	require.Equal(inr.T(), originalRole.InheritedNamespacedRules, persistedRole.InheritedNamespacedRules,
		"A rejected update must leave the original inherited namespaced rules unchanged")
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateInDifferentNamespaceReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with only get/list permissions cannot delegate inherited namespaced permissions to a different namespace")

	user1Client, _, sharedNamespace, otherNamespace, limitedRules, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	roleName := namegen.AppendRandomString("t4-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			otherNamespace: limitedRules[sharedNamespace],
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotCreateInDifferentNamespaceEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with edit permission cannot delegate inherited namespaced permissions to a different namespace")

	user1Client, _, sharedNamespace, otherNamespace, limitedRules, _ := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.EditVerb})

	roleName := namegen.AppendRandomString("t4-gr-")
	_, err := extrbacapi.CreateGlobalRole(user1Client, &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{Name: roleName},
		InheritedNamespacedRules: map[string][]rbacv1.PolicyRule{
			otherNamespace: limitedRules[sharedNamespace],
		},
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = rbacapi.GetGlobalRoleByName(inr.client, roleName)
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotBindMorePrivilegedRoleToAnotherUserReadPermissions() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with only get/list permissions cannot bind a more privileged GlobalRole to another user")

	user1Client, user2, _, _, _, adminOnlyRole := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.GetVerb, rbacapi.ListVerb})

	bindingName := namegen.AppendRandomString("t5-grb-")
	_, err := extrbacapi.CreateGlobalRoleBinding(user1Client, &v3.GlobalRoleBinding{
		ObjectMeta:     metav1.ObjectMeta{Name: bindingName},
		GlobalRoleName: adminOnlyRole.Name,
		UserName:       user2.Name,
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(bindingName, metav1.GetOptions{})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func (inr *InheritedNamespacedRulesEscalationTestSuite) TestCannotBindMorePrivilegedRoleToAnotherUserEditPermission() {
	subSession := inr.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying a user with edit permission cannot bind a more privileged GlobalRole to another user")

	user1Client, user2, _, _, _, adminOnlyRole := inr.setupEscalation(rbacapi.CatalogCattleAPIGroup, rbacapi.AppsResource, []string{rbacapi.EditVerb})

	bindingName := namegen.AppendRandomString("t5-grb-")
	_, err := extrbacapi.CreateGlobalRoleBinding(user1Client, &v3.GlobalRoleBinding{
		ObjectMeta:     metav1.ObjectMeta{Name: bindingName},
		GlobalRoleName: adminOnlyRole.Name,
		UserName:       user2.Name,
	})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsForbidden(err), "Expected Forbidden, got %v", err)

	_, err = inr.client.WranglerContext.Mgmt.GlobalRoleBinding().Get(bindingName, metav1.GetOptions{})
	require.Error(inr.T(), err)
	require.True(inr.T(), apierrors.IsNotFound(err), "Expected NotFound, got %v", err)
}

func TestInheritedNamespacedRulesEscalationTestSuite(t *testing.T) {
	suite.Run(t, new(InheritedNamespacedRulesEscalationTestSuite))
}

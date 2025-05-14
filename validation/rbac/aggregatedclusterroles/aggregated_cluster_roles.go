package aggregatedclusterroles

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/secrets"
	"github.com/rancher/tests/actions/workloads/deployment"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	clusterMgmtResource = "-cluster-mgmt"
	projectMgmtResource = "-project-mgmt"
)

var clusterMgmtResources = map[string]string{
	"clusterscans":                rbac.ManagementAPIGroup,
	"clusterregistrationtokens":   rbac.ManagementAPIGroup,
	"clusterroletemplatebindings": rbac.ManagementAPIGroup,
	"etcdbackups":                 rbac.ManagementAPIGroup,
	"nodes":                       rbac.ManagementAPIGroup,
	"nodepools":                   rbac.ManagementAPIGroup,
	"projects":                    rbac.ManagementAPIGroup,
	"etcdsnapshots":               rbac.RkeCattleAPIGroup,
}

var projectMgmtResources = map[string]string{
	"sourcecodeproviderconfigs":   rbac.ProjectCattleAPIGroup,
	"projectroletemplatebindings": rbac.ManagementAPIGroup,
	"secrets":                     "",
}

var policyRules = map[string][]rbacv1.PolicyRule{
	"readProjects":    definePolicyRules([]string{"get", "list"}, []string{"projects"}, []string{rbac.ManagementAPIGroup}),
	"editProjects":    definePolicyRules([]string{"create", "update", "patch"}, []string{"projects"}, []string{rbac.ManagementAPIGroup}),
	"manageProjects":  definePolicyRules([]string{"create", "update", "patch", "delete"}, []string{"projects"}, []string{rbac.ManagementAPIGroup}),
	"readPrtbs":       definePolicyRules([]string{"get", "list"}, []string{"projectroletemplatebindings"}, []string{rbac.ManagementAPIGroup}),
	"updatePrtbs":     definePolicyRules([]string{"update", "patch"}, []string{"projectroletemplatebindings"}, []string{rbac.ManagementAPIGroup}),
	"readDeployments": definePolicyRules([]string{"get", "list"}, []string{"deployments"}, []string{rbac.AppsAPIGroup}),
	"readPods":        definePolicyRules([]string{"get", "list"}, []string{"pods"}, []string{""}),
	"readNamespaces":  definePolicyRules([]string{"get", "list"}, []string{"namespaces"}, []string{""}),
	"readSecrets":     definePolicyRules([]string{"get", "list"}, []string{"secrets"}, []string{""}),
}

func definePolicyRules(verbs, resources, apiGroups []string) []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{{
		Verbs:     verbs,
		Resources: resources,
		APIGroups: apiGroups,
	}}
}

func acrCreateTestResources(client *rancher.Client, cluster *management.Cluster) (*v3.Project, []*corev1.Namespace, *management.User, []*appsv1.Deployment, []string, []*corev1.Secret, error) {
	createdProject, err := projects.CreateProjectUsingWrangler(client, cluster.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to create project: %w", err)
	}

	downstreamContext, err := clusterapi.GetClusterWranglerContext(client, cluster.ID)
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to get downstream context: %w", err)
	}

	var createdNamespaces []*corev1.Namespace
	var createdDeployments []*appsv1.Deployment
	var createdSecrets []*corev1.Secret
	var podNames []string

	numNamespaces := 2
	for i := 0; i < numNamespaces; i++ {
		namespace, err := projects.CreateNamespaceUsingWrangler(client, cluster.ID, createdProject.Name)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to create namespace %d: %w", i+1, err)
		}
		createdNamespaces = append(createdNamespaces, namespace)

		createdDeployment, err := deployment.CreateDeployment(client, cluster.ID, namespace.Name, 2, "", "", false, false, false, true)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to create deployment in namespace %s: %w", namespace.Name, err)
		}
		createdDeployments = append(createdDeployments, createdDeployment)

		podList, err := downstreamContext.Core.Pod().List(namespace.Name, metav1.ListOptions{})
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to list pods in namespace %s: %w", namespace.Name, err)
		}
		if len(podList.Items) == 0 {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("no pods found in namespace %s", namespace.Name)
		}
		podNames = append(podNames, podList.Items[0].Name)

		secretData := map[string][]byte{
			"hello": []byte("world"),
		}
		createdSecret, err := secrets.CreateSecret(client, cluster.ID, namespace.Name, secretData, corev1.SecretTypeOpaque)
		if err != nil {
			return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to create secret in namespace %s: %w", namespace.Name, err)
		}
		createdSecrets = append(createdSecrets, createdSecret)
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String())
	if err != nil {
		return nil, nil, nil, nil, nil, nil, fmt.Errorf("failed to create standard user: %w", err)
	}

	return createdProject, createdNamespaces, createdUser, createdDeployments, podNames, createdSecrets, nil
}

func getClusterRolesForRoleTemplates(client *rancher.Client, clusterID string, rtNames ...string) (*rbacv1.ClusterRoleList, error) {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	var clusterRoles rbacv1.ClusterRoleList

	for _, rtName := range rtNames {
		names := []string{rtName, rtName + rbac.RegularResourceAggregator}

		if isMgmtResource(client, rtName, rbac.ClusterContext) {
			names = append(names, rtName+clusterMgmtResource, rtName+rbac.ClusterMgmtResourceAggregator)
		} else if isMgmtResource(client, rtName, rbac.ProjectContext) {
			names = append(names, rtName+projectMgmtResource, rtName+rbac.ProjectMgmtResourceAggregator)
		}

		for _, name := range names {
			cr, err := ctx.RBAC.ClusterRole().Get(name, metav1.GetOptions{})
			if err != nil {
				if !apierrors.IsNotFound(err) {
					return nil, err
				}
				continue
			}
			clusterRoles.Items = append(clusterRoles.Items, *cr)
		}
	}

	return &clusterRoles, nil
}

func isMgmtResource(client *rancher.Client, rtName string, resourceContext string) bool {
	rt, err := client.WranglerContext.Mgmt.RoleTemplate().Get(rtName, metav1.GetOptions{})
	if err != nil {
		return false
	}

	for _, rule := range rt.Rules {
		if isMgmtRule(rule, resourceContext) {
			return true
		}
	}

	return false
}

func isMgmtRule(rule rbacv1.PolicyRule, resourceContext string) bool {
	resourceMap := clusterMgmtResources
	if resourceContext == rbac.ProjectContext {
		resourceMap = projectMgmtResources
	}

	for _, group := range rule.APIGroups {
		if (resourceContext == rbac.ClusterContext && (group == rbac.ManagementAPIGroup || group == rbac.RkeCattleAPIGroup)) ||
			(resourceContext == rbac.ProjectContext && (group == rbac.ProjectCattleAPIGroup || group == rbac.ManagementAPIGroup || group == "")) {
			for _, resource := range rule.Resources {
				if _, ok := resourceMap[resource]; ok {
					return true
				}
			}
		}
	}

	return false
}

func verifyMainACRContainsAllRules(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	mainRules, err := rbac.GetClusterRoleRules(client, clusterID, mainRTName)
	if err != nil {
		return fmt.Errorf("failed to get mainRole rules: %w", err)
	}

	var allChildRules []rbacv1.PolicyRule
	for _, childRTName := range childRTNames {
		childRules, err := rbac.GetClusterRoleRules(client, clusterID, childRTName)
		if err != nil {
			return fmt.Errorf("failed to get childRole rules %s: %w", childRTName, err)
		}
		allChildRules = append(allChildRules, childRules...)
	}

	expectedRules := append(mainRules, allChildRules...)

	acrNameRegular := mainRTName + rbac.RegularResourceAggregator
	actualRulesRegular, err := rbac.GetClusterRoleRules(client, clusterID, acrNameRegular)
	if err != nil {
		return fmt.Errorf("failed to get ACR %s: %w", acrNameRegular, err)
	}

	if !ruleSlicesMatch(actualRulesRegular, expectedRules) {
		return fmt.Errorf("ACR %s rules do not match expected combined rules", acrNameRegular)
	}

	return nil
}

func ruleSlicesMatch(rules1, rules2 []rbacv1.PolicyRule) bool {
	rules1Copy := slices.Clone(rules1)
	rules2Copy := slices.Clone(rules2)

	slices.SortFunc(rules1Copy, comparePolicyRules)
	slices.SortFunc(rules2Copy, comparePolicyRules)

	return reflect.DeepEqual(rules1Copy, rules2Copy)
}

func comparePolicyRules(a, b rbacv1.PolicyRule) int {
	if cmp := compareSlices(a.Verbs, b.Verbs); cmp != 0 {
		return cmp
	}
	if cmp := compareSlices(a.APIGroups, b.APIGroups); cmp != 0 {
		return cmp
	}
	if cmp := compareSlices(a.Resources, b.Resources); cmp != 0 {
		return cmp
	}
	if cmp := compareSlices(a.ResourceNames, b.ResourceNames); cmp != 0 {
		return cmp
	}
	return compareSlices(a.NonResourceURLs, b.NonResourceURLs)
}

func compareSlices(a, b []string) int {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}

	for i := 0; i < minLen; i++ {
		if a[i] < b[i] {
			return -1
		} else if a[i] > b[i] {
			return 1
		}
	}
	return len(a) - len(b)
}

func verifyClusterMgmtACR(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	acrName := mainRTName + rbac.ClusterMgmtResourceAggregator
	return verifyMgmtACR(client, clusterID, acrName, mainRTName, childRTNames, rbac.ClusterContext)
}

func verifyProjectMgmtACR(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	acrName := mainRTName + rbac.ProjectMgmtResourceAggregator
	return verifyMgmtACR(client, clusterID, acrName, mainRTName, childRTNames, rbac.ProjectContext)
}

func verifyMgmtACR(client *rancher.Client, clusterID, acrName, mainRTName string, childRTNames []string, managementContext string) error {
	mainRules, err := rbac.GetClusterRoleRules(client, clusterID, mainRTName)
	if err != nil {
		return err
	}

	allChildRules := []rbacv1.PolicyRule{}
	for _, childRTName := range childRTNames {
		childRules, err := rbac.GetClusterRoleRules(client, clusterID, childRTName)
		if err != nil {
			return err
		}
		allChildRules = append(allChildRules, childRules...)
	}

	expectedRules := append(mainRules, allChildRules...)
	mgmtRules := filterMgmtRules(expectedRules, managementContext)

	acrRules, err := rbac.GetClusterRoleRules(client, clusterID, acrName)
	if err != nil {
		return fmt.Errorf("failed to get ACR %s: %w", acrName, err)
	}

	if !ruleSlicesMatch(acrRules, mgmtRules) {
		return fmt.Errorf("ACR %s rules do not match expected combined rules.\nExpected: %+v\nActual: %+v", acrName, mgmtRules, acrRules)
	}

	return nil
}

func filterMgmtRules(rules []rbacv1.PolicyRule, mgmtType string) []rbacv1.PolicyRule {
	var filteredRules []rbacv1.PolicyRule
	for _, rule := range rules {
		if (mgmtType == rbac.ClusterContext && isMgmtRule(rule, rbac.ClusterContext)) || (mgmtType == rbac.ProjectContext && isMgmtRule(rule, rbac.ProjectContext)) {
			filteredRules = append(filteredRules, rule)
		}
	}
	return filteredRules
}

func verifyBindings(client *rancher.Client, clusterID string, userName string, roleTemplateName string, roleTemplateBindingName string, namespaces []string, expectedRoleBindingCount, expectedClusterRoleBindingCount int) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	for _, namespace := range namespaces {
		roleBindings, err := ctx.RBAC.RoleBinding().List(namespace, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list RoleBindings in namespace %s: %w", namespace, err)
		}

		filteredRBs := filterRoleBindings(roleBindings, userName, roleTemplateName)

		if len(filteredRBs) != expectedRoleBindingCount {
			return fmt.Errorf("expected %d RoleBindings for user %s in namespace %s, but got %d", expectedRoleBindingCount, userName, namespace, len(filteredRBs))
		}

		if expectedRoleBindingCount > 0 {
			expectedRoleName := roleTemplateName + rbac.RegularResourceAggregator

			if filteredRBs[0].RoleRef.Name != expectedRoleName {
				return fmt.Errorf("expected RoleRef.Name %s, but got %s", expectedRoleName, filteredRBs[0].RoleRef.Name)
			}
		}
	}

	clusterRoleBindings, err := ctx.RBAC.ClusterRoleBinding().List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ClusterRoleBindings in the local cluster: %w", err)
	}

	filteredCRBs := filterClusterRoleBindings(clusterRoleBindings, userName, roleTemplateName)

	if len(filteredCRBs) != expectedClusterRoleBindingCount {
		return fmt.Errorf("expected %d ClusterRoleBindings, but got %d", expectedClusterRoleBindingCount, len(filteredCRBs))
	}

	if expectedClusterRoleBindingCount > 0 {
		var expectedRoleName string
		if clusterID == rbacapi.LocalCluster {
			if strings.Contains(roleTemplateBindingName, "prtb") {
				expectedRoleName = roleTemplateName + rbac.ProjectMgmtResourceAggregator
			} else {
				expectedRoleName = roleTemplateName + rbac.ClusterMgmtResourceAggregator
			}
		} else {
			expectedRoleName = roleTemplateName + rbac.RegularResourceAggregator
		}

		if filteredCRBs[0].RoleRef.Name != expectedRoleName {
			return fmt.Errorf("expected RoleRef.Name %s, but got %s", expectedRoleName, filteredCRBs[0].RoleRef.Name)
		}
	}

	return nil
}

func filterRoleBindings(roleBindings *rbacv1.RoleBindingList, userName, roleTemplateName string) []rbacv1.RoleBinding {
	var filteredRBs []rbacv1.RoleBinding
	re := regexp.MustCompile("^" + regexp.QuoteMeta(roleTemplateName))

	for _, rb := range roleBindings.Items {
		for _, subject := range rb.Subjects {
			if subject.Kind == rbacv1.UserKind && subject.Name == userName && re.MatchString(rb.RoleRef.Name) {
				filteredRBs = append(filteredRBs, rb)
			}
		}
	}
	return filteredRBs
}

func filterClusterRoleBindings(clusterRoleBindings *rbacv1.ClusterRoleBindingList, userName, roleTemplateName string) []rbacv1.ClusterRoleBinding {
	var filteredCRBs []rbacv1.ClusterRoleBinding
	re := regexp.MustCompile("^" + regexp.QuoteMeta(roleTemplateName))

	for _, rb := range clusterRoleBindings.Items {
		for _, subject := range rb.Subjects {
			if subject.Kind == rbacv1.UserKind && subject.Name == userName && re.MatchString(rb.RoleRef.Name) {
				filteredCRBs = append(filteredCRBs, rb)
			}
		}
	}
	return filteredCRBs
}

func verifyBindingsForCrtb(client *rancher.Client, clusterID string, crtb *v3.ClusterRoleTemplateBinding, expectedRoleBindingCount, expectedClusterRoleBindingCount int) error {
	return verifyBindings(client, clusterID, crtb.UserName, crtb.RoleTemplateName, crtb.Name, []string{clusterID}, expectedRoleBindingCount, expectedClusterRoleBindingCount)
}

func verifyBindingsForPrtb(client *rancher.Client, clusterID string, prtb *v3.ProjectRoleTemplateBinding, namespaces []*corev1.Namespace, expectedRoleBindingCount, expectedClusterRoleBindingCount int) error {
	namespace := strings.SplitN(prtb.ProjectName, ":", 2)[0]
	namespaceNames := []string{}

	if namespaces == nil {
		namespaceNames = append(namespaceNames, namespace)
	} else {
		for _, ns := range namespaces {
			namespaceNames = append(namespaceNames, ns.Name)
		}
	}
	return verifyBindings(client, clusterID, prtb.UserName, prtb.RoleTemplateName, prtb.Name, namespaceNames, expectedRoleBindingCount, expectedClusterRoleBindingCount)
}

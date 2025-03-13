package rbac

import (
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/pkg/wrangler"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// VerifyClusterRoleTemplateBindingForUser is a helper function to verify the number of cluster role template bindings for a user
func VerifyClusterRoleTemplateBindingForUser(client *rancher.Client, username string, expectedCount int) ([]v3.ClusterRoleTemplateBinding, error) {
	crtbList, err := ListClusterRoleTemplateBindings(client, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ClusterRoleTemplateBindings: %w", err)
	}

	userCrtbs := []v3.ClusterRoleTemplateBinding{}
	actualCount := 0
	for _, crtb := range crtbList.Items {
		if crtb.UserName == username {
			userCrtbs = append(userCrtbs, crtb)
			actualCount++
		}
	}

	if actualCount != expectedCount {
		return nil, fmt.Errorf("expected %d ClusterRoleTemplateBindings for user %s, but found %d",
			expectedCount, username, actualCount)
	}

	return userCrtbs, nil
}

// VerifyProjectRoleTemplateBindingForUser is a helper function to verify the number of project role template bindings for a user
func VerifyProjectRoleTemplateBindingForUser(client *rancher.Client, username string, expectedCount int) ([]v3.ProjectRoleTemplateBinding, error) {
	prtbList, err := ListProjectRoleTemplateBindings(client, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ProjectRoleTemplateBindings: %w", err)
	}

	userPrtbs := []v3.ProjectRoleTemplateBinding{}
	actualCount := 0
	for _, prtb := range prtbList.Items {
		if prtb.UserName == username {
			userPrtbs = append(userPrtbs, prtb)
			actualCount++
		}
	}

	if actualCount != expectedCount {
		return nil, fmt.Errorf("expected %d ProjectRoleTemplateBindings for user %s, but found %d",
			expectedCount, username, actualCount)
	}

	return userPrtbs, nil
}

// VerifyRoleRules checks if the expected role rules match the actual rules.
func VerifyRoleRules(expected, actual map[string][]string) error {
	for resource, expectedVerbs := range expected {
		actualVerbs, exists := actual[resource]
		if !exists {
			return fmt.Errorf("resource %s not found in role rules", resource)
		}

		expectedSet := make(map[string]struct{})
		for _, verb := range expectedVerbs {
			expectedSet[verb] = struct{}{}
		}

		for _, verb := range actualVerbs {
			if _, found := expectedSet[verb]; !found {
				return fmt.Errorf("verbs for resource %s do not match: expected %v, got %v", resource, expectedVerbs, actualVerbs)
			}
		}
	}
	return nil
}

// VerifyUserPermission validates that a user has the expected permissions for a given resource
func VerifyUserPermission(client *rancher.Client, clusterID string, user *management.User, verb, resourceType, namespaceName, resourceName string, expected, isCRDInLocalCluster bool) error {
	allowed, err := CheckUserAccess(client, clusterID, user, verb, resourceType, namespaceName, resourceName, isCRDInLocalCluster)

	if expected {
		if err != nil {
			if apierrors.IsForbidden(err) {
				return fmt.Errorf("user should have '%s' access to %s/%s/%s, but got forbidden error: %v", verb, resourceType, namespaceName, resourceName, err)
			}
			return fmt.Errorf("error verifying user access to %s/%s/%s: %v", resourceType, namespaceName, resourceName, err)
		}
		if !allowed {
			return fmt.Errorf("user should have '%s' access to %s/%s/%s, but access was denied", verb, resourceType, namespaceName, resourceName)
		}
	} else {
		if err == nil && allowed {
			return fmt.Errorf("expected '%s' access to %s/%s/%s to be denied, but access was granted", verb, resourceType, namespaceName, resourceName)
		}
		if err != nil && !apierrors.IsForbidden(err) {
			return fmt.Errorf("expected forbidden error for %s/%s/%s, but got: %v", resourceType, namespaceName, resourceName, err)
		}
	}

	return nil
}

// CheckUserAccess checks if a user has the specified access to a resource in a cluster. It returns true if the user has access, false otherwise.
func CheckUserAccess(client *rancher.Client, clusterID string, user *management.User, verb, resourceType, namespaceName, resourceName string, isCRDInLocalCluster bool) (bool, error) {
	userClient, err := client.AsUser(user)
	if err != nil {
		return false, fmt.Errorf("failed to create user client: %w", err)
	}

	var userContext *wrangler.Context
	if isCRDInLocalCluster {
		userContext, err = clusterapi.GetClusterWranglerContext(userClient, clusterapi.LocalCluster)
	} else {
		userContext, err = clusterapi.GetClusterWranglerContext(userClient, clusterID)
	}

	if err != nil {
		return false, fmt.Errorf("failed to get user context: %w", err)
	}

	switch resourceType {
	case "projects":
		return CheckProjectAccess(userContext, verb, clusterID, resourceName)
	case "namespaces":
		return CheckNamespaceAccess(userContext, verb, resourceName)
	case "deployments":
		return CheckDeploymentAccess(userContext, verb, namespaceName, resourceName)
	case "pods":
		return CheckPodAccess(userContext, verb, namespaceName, resourceName)
	case "secrets":
		return CheckSecretAccess(userContext, verb, namespaceName, resourceName)
	case "projectroletemplatebindings":
		return CheckPrtbAccess(userContext, verb, namespaceName, resourceName)
	case "configmaps":
		return CheckConfigMapAccess(userContext, verb, namespaceName, resourceName)
	default:
		return false, fmt.Errorf("checks for resource type '%s' not added", resourceType)
	}
}

// CheckProjectAccess checks if a user has the specified access to a project in a cluster. It returns true if the user has access, false otherwise.
func CheckProjectAccess(userContext *wrangler.Context, verb, clusterID, projectName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Mgmt.Project().List(clusterID, metav1.ListOptions{})
		return err == nil, err
	case "create":
		projectTemplate := projectapi.NewProjectTemplate(clusterID)
		_, err := userContext.Mgmt.Project().Create(projectTemplate)
		return err == nil, err
	case "delete":
		err := userContext.Mgmt.Project().Delete(clusterID, projectName, &metav1.DeleteOptions{})
		return err == nil, err
	case "update":
		project, err := userContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if project.Labels == nil {
			project.Labels = make(map[string]string)
		}
		project.Labels["hello"] = "world"
		_, err = userContext.Mgmt.Project().Update(project)
		return err == nil, err
	case "patch":
		patchData := []byte(`{"metadata":{"annotations":{"patched":"true"}}}`)
		_, err := userContext.Mgmt.Project().Patch(clusterID, projectName, types.MergePatchType, patchData)
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for projects", verb)
	}
}

// CheckNamespaceAccess checks if a user has the specified access to a namespace in a cluster. It returns true if the user has access, false otherwise.
func CheckNamespaceAccess(userContext *wrangler.Context, verb, namespaceName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.Namespace().List(metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.Namespace().Delete(namespaceName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'namespaces'", verb)
	}
}

// CheckPodAccess checks if a user has the specified access to a pod in a namespace. It returns true if the user has access, false otherwise.
func CheckPodAccess(userContext *wrangler.Context, verb, namespaceName, podName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.Pod().Get(namespaceName, podName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.Pod().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.Pod().Delete(namespaceName, podName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'pods'", verb)
	}
}

// CheckDeploymentAccess checks if a user has the specified access to a deployment in a namespace. It returns true if the user has access, false otherwise.
func CheckDeploymentAccess(userContext *wrangler.Context, verb, namespaceName, deploymentName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Apps.Deployment().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Apps.Deployment().Delete(namespaceName, deploymentName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'deployments'", verb)
	}
}

// CheckSecretAccess checks if a user has the specified access to a secret in a namespace. It returns true if the user has access, false otherwise.
func CheckSecretAccess(userContext *wrangler.Context, verb, namespaceName, secretName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.Secret().Get(namespaceName, secretName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.Secret().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.Secret().Delete(namespaceName, secretName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'namespaces'", verb)
	}
}

// CheckPrtbAccess checks if a user has the specified access to a project role template binding in a namespace. It returns true if the user has access, false otherwise.
func CheckPrtbAccess(userContext *wrangler.Context, verb, prtbNamespace, prtbName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbNamespace, prtbName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().List(prtbNamespace, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Mgmt.ProjectRoleTemplateBinding().Delete(prtbNamespace, prtbName, &metav1.DeleteOptions{})
		return err == nil, err
	case "update":
		prtb, err := userContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbNamespace, prtbName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if prtb.Labels == nil {
			prtb.Labels = make(map[string]string)
		}
		prtb.Labels["hello"] = "world"
		_, err = userContext.Mgmt.ProjectRoleTemplateBinding().Update(prtb)
		return err == nil, err
	case "patch":
		patchData := []byte(`{"metadata":{"annotations":{"patched":"true"}}}`)
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().Patch(prtbNamespace, prtbName, types.MergePatchType, patchData)
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for prtbs", verb)
	}
}

// CheckConfigMapAccess checks if a user has the specified access to a ConfigMap in a namespace. It returns true if the user has access, false otherwise.
func CheckConfigMapAccess(userContext *wrangler.Context, verb, namespaceName, configMapName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.ConfigMap().Get(namespaceName, configMapName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.ConfigMap().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.ConfigMap().Delete(namespaceName, configMapName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'configmaps'", verb)
	}
}

// VerifyBindingsForCrtb verifies RoleBindings and ClusterRoleBindings for a given CRTB
func VerifyBindingsForCrtb(client *rancher.Client, clusterID string, crtb *v3.ClusterRoleTemplateBinding, expectedRoleBindingCount, expectedClusterRoleBindingCount int) error {
	return verifyBindings(client, clusterID, crtb.UserName, crtb.RoleTemplateName, crtb.Name, []string{crtb.Namespace}, expectedRoleBindingCount, expectedClusterRoleBindingCount)
}

// VerifyBindingsForPrtb verifies RoleBindings and ClusterRoleBindings for a given PRTB
func VerifyBindingsForPrtb(client *rancher.Client, clusterID string, prtb *v3.ProjectRoleTemplateBinding, namespaces []*corev1.Namespace, expectedRoleBindingCount, expectedClusterRoleBindingCount int) error {
	namespaceNames := []string{}
	defaultNamespace := strings.SplitN(prtb.ProjectName, ":", 2)[0]

	if len(namespaces) == 0 {
		namespaceNames = append(namespaceNames, defaultNamespace)
	} else {
		for _, ns := range namespaces {
			namespaceNames = append(namespaceNames, ns.Name)
		}
	}

	return verifyBindings(client, clusterID, prtb.UserName, prtb.RoleTemplateName, prtb.Name, namespaceNames, expectedRoleBindingCount, expectedClusterRoleBindingCount)
}

// VerifyMainACRContainsAllRules verifies that the main ACR contains all rules from the main role template and its child role templates
func VerifyMainACRContainsAllRules(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	mainRules, err := GetClusterRoleRules(client, clusterID, mainRTName)
	if err != nil {
		return fmt.Errorf("failed to get mainRole rules: %w", err)
	}

	var allChildRules []rbacv1.PolicyRule
	for _, childRTName := range childRTNames {
		childRules, err := GetClusterRoleRules(client, clusterID, childRTName)
		if err != nil {
			return fmt.Errorf("failed to get childRole rules %s: %w", childRTName, err)
		}
		allChildRules = append(allChildRules, childRules...)
	}

	expectedRules := append(mainRules, allChildRules...)

	acrNameRegular := mainRTName + ResourceAggregator
	actualRulesRegular, err := GetClusterRoleRules(client, clusterID, acrNameRegular)
	if err != nil {
		return fmt.Errorf("failed to get ACR %s: %w", acrNameRegular, err)
	}

	if !ruleSlicesMatch(actualRulesRegular, expectedRules) {
		return fmt.Errorf("ACR %s rules do not match expected combined rules", acrNameRegular)
	}

	return nil
}

// VerifyClusterMgmtACR verifies that the cluster management ACR contains all rules from the main role template and its child role templates
func VerifyClusterMgmtACR(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	acrName := mainRTName + ClusterMgmtResourceAggregator
	return verifyMgmtACR(client, clusterID, acrName, mainRTName, childRTNames, ClusterContext)
}

// VerifyProjectMgmtACR verifies that the project management ACR contains all rules from the main role template and its child role templates
func VerifyProjectMgmtACR(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	acrName := mainRTName + ProjectMgmtResourceAggregator
	return verifyMgmtACR(client, clusterID, acrName, mainRTName, childRTNames, ProjectContext)
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

func verifyMgmtACR(client *rancher.Client, clusterID, acrName, mainRTName string, childRTNames []string, managementContext string) error {
	mainRules, err := GetClusterRoleRules(client, clusterID, mainRTName)
	if err != nil {
		return err
	}

	allChildRules := []rbacv1.PolicyRule{}
	for _, childRTName := range childRTNames {
		childRules, err := GetClusterRoleRules(client, clusterID, childRTName)
		if err != nil {
			return err
		}
		allChildRules = append(allChildRules, childRules...)
	}

	expectedRules := append(mainRules, allChildRules...)
	mgmtRules := filterMgmtRules(expectedRules, managementContext)

	acrRules, err := GetClusterRoleRules(client, clusterID, acrName)
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
		if (mgmtType == ClusterContext && isMgmtRule(rule, ClusterContext)) || (mgmtType == ProjectContext && isMgmtRule(rule, ProjectContext)) {
			filteredRules = append(filteredRules, rule)
		}
	}
	return filteredRules
}

func isMgmtRule(rule rbacv1.PolicyRule, resourceContext string) bool {
	resourceMap := ClusterMgmtResources
	if resourceContext == ProjectContext {
		resourceMap = ProjectMgmtResources
	}

	for _, group := range rule.APIGroups {
		if (resourceContext == ClusterContext && (group == ManagementAPIGroup || group == RkeCattleAPIGroup)) ||
			(resourceContext == ProjectContext && (group == ProjectCattleAPIGroup || group == ManagementAPIGroup || group == "")) {
			for _, resource := range rule.Resources {
				if _, ok := resourceMap[resource]; ok {
					return true
				}
			}
		}
	}

	return false
}

func verifyBindings(client *rancher.Client, clusterID, userName, roleTemplateName, roleTemplateBindingName string, namespaces []string, expectedRBCount, expectedCRBCount int) error {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	for _, ns := range namespaces {
		rbs, err := ctx.RBAC.RoleBinding().List(ns, metav1.ListOptions{})
		if err != nil {
			return fmt.Errorf("failed to list RoleBindings in namespace %s: %w", ns, err)
		}

		filtered := filterRoleBindings(rbs, userName, roleTemplateName)
		if len(filtered) != expectedRBCount {
			return fmt.Errorf("expected %d RoleBindings for user %s in namespace %s, got %d",
				expectedRBCount, userName, ns, len(filtered))
		}

		if expectedRBCount > 0 {
			expected := expectedRoleNames(clusterID, roleTemplateBindingName, roleTemplateName, expectedRBCount)
			expectedSet := make(map[string]struct{}, len(expected))
			for _, name := range expected {
				expectedSet[name] = struct{}{}
			}

			for _, rb := range filtered {
				if _, ok := expectedSet[rb.RoleRef.Name]; !ok {
					return fmt.Errorf("unexpected RoleBinding RoleRef.Name %s, expected %v",
						rb.RoleRef.Name, expected)
				}
			}
		}
	}

	crbs, err := ctx.RBAC.ClusterRoleBinding().List(metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list ClusterRoleBindings: %w", err)
	}

	filteredCRBs := filterClusterRoleBindings(crbs, userName, roleTemplateName)
	if len(filteredCRBs) != expectedCRBCount {
		return fmt.Errorf("expected %d ClusterRoleBindings, got %d",
			expectedCRBCount, len(filteredCRBs))
	}

	if expectedCRBCount > 0 {
		expected := expectedRoleNames(clusterID, roleTemplateBindingName, roleTemplateName, expectedCRBCount)
		expectedSet := make(map[string]struct{}, len(expected))
		for _, name := range expected {
			expectedSet[name] = struct{}{}
		}

		for _, crb := range filteredCRBs {
			if _, ok := expectedSet[crb.RoleRef.Name]; !ok {
				return fmt.Errorf("unexpected ClusterRoleBinding RoleRef.Name %s, expected %v",
					crb.RoleRef.Name, expected)
			}
		}
	}

	return nil
}

func expectedRoleNames(clusterID, bindingName, rtName string, count int) []string {
	if clusterID != clusterapi.LocalCluster {
		return []string{rtName + ResourceAggregator}
	}

	if strings.Contains(bindingName, "prtb") {
		return []string{rtName + ProjectMgmtResourceAggregator}
	}

	roles := []string{rtName + ClusterMgmtResourceAggregator}
	if count > 1 {
		roles = append(roles, rtName+ProjectMgmtResourceAggregator)
	}
	return roles
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

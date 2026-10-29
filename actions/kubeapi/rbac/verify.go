package rbac

import (
	"context"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wrangler"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	podsapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	appsv1 "k8s.io/api/apps/v1"
	authzv1 "k8s.io/api/authorization/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	authzclient "k8s.io/client-go/kubernetes/typed/authorization/v1"
)

const allNamespaces = ""

// VerifyClusterRoleTemplateBindingForUser is a helper function to verify the number of cluster role template bindings for a user
func VerifyClusterRoleTemplateBindingForUser(client *rancher.Client, username string, expectedCount int) ([]v3.ClusterRoleTemplateBinding, error) {
	crtbList, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().List("", metav1.ListOptions{})
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
	prtbList, err := client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List("", metav1.ListOptions{})
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
func VerifyUserPermission(userClient *rancher.Client, clusterID string, verb, resourceType, namespaceName, resourceName string, expected, isCRDInLocalCluster bool) error {
	allowed, err := CheckUserAccess(userClient, clusterID, verb, resourceType, namespaceName, resourceName, isCRDInLocalCluster)

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
func CheckUserAccess(userClient *rancher.Client, clusterID string, verb, resourceType, namespaceName, resourceName string, isCRDInLocalCluster bool) (bool, error) {
	var userContext *wrangler.Context
	var err error
	if isCRDInLocalCluster {
		userContext, err = extclusterapi.GetClusterWranglerContext(userClient, extclusterapi.LocalCluster)
	} else {
		userContext, err = extclusterapi.GetClusterWranglerContext(userClient, clusterID)
	}

	if err != nil {
		return false, fmt.Errorf("failed to get user context: %w", err)
	}

	switch resourceType {
	case ProjectsResource:
		return CheckProjectAccess(userContext, verb, clusterID, resourceName)
	case NamespaceResource:
		return CheckNamespaceAccess(userContext, verb, resourceName)
	case DeploymentsResource:
		return CheckDeploymentAccess(userContext, verb, namespaceName, resourceName)
	case PodsResource:
		return CheckPodAccess(userContext, verb, namespaceName, resourceName)
	case SecretsResource:
		return CheckSecretAccess(userContext, verb, namespaceName, resourceName)
	case PrtbResource:
		return CheckPrtbAccess(userContext, verb, namespaceName, resourceName)
	case ConfigMapResource:
		return CheckConfigMapAccess(userContext, verb, namespaceName, resourceName)
	case StatefulSetResource:
		return CheckStatefulSetAccess(userContext, verb, namespaceName, resourceName)
	case NodesResource:
		return CheckNodeAccess(userContext, verb, resourceName)
	default:
		return false, fmt.Errorf("checks for resource type '%s' not added", resourceType)
	}
}

// CheckProjectAccess checks if a user has the specified access to a project in a cluster. It returns true if the user has access, false otherwise.
func CheckProjectAccess(userContext *wrangler.Context, verb, clusterID, projectName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Mgmt.Project().List(clusterID, metav1.ListOptions{})
		return err == nil, err
	case CreateVerb:
		projectTemplate := projectapi.NewProjectTemplate(clusterID)
		_, err := userContext.Mgmt.Project().Create(projectTemplate)
		return err == nil, err
	case DeleteVerb:
		err := userContext.Mgmt.Project().Delete(clusterID, projectName, &metav1.DeleteOptions{})
		return err == nil, err
	case UpdateVerb:
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
	case PatchVerb:
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
	case GetVerb:
		_, err := userContext.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Core.Namespace().List(metav1.ListOptions{})
		return err == nil, err
	case DeleteVerb:
		err := userContext.Core.Namespace().Delete(namespaceName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'namespaces'", verb)
	}
}

// CheckPodAccess checks if a user has the specified access to a pod in a namespace. It returns true if the user has access, false otherwise.
func CheckPodAccess(userContext *wrangler.Context, verb, namespaceName, podName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Core.Pod().Get(namespaceName, podName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Core.Pod().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case CreateVerb:
		podTemplate := podsapi.CreateContainerAndPodTemplate("")
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      namegen.AppendRandomString("testpod-"),
				Namespace: namespaceName,
			},
			Spec: podTemplate.Spec,
		}
		_, err := userContext.Core.Pod().Create(pod)
		return err == nil, err
	case DeleteVerb:
		err := userContext.Core.Pod().Delete(namespaceName, podName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'pods'", verb)
	}
}

// CheckDeploymentAccess checks if a user has the specified access to a deployment in a namespace. It returns true if the user has access, false otherwise.
func CheckDeploymentAccess(userContext *wrangler.Context, verb, namespaceName, deploymentName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Apps.Deployment().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case CreateVerb:
		deploymentName := namegen.AppendRandomString("testdeployment-")
		podTemplate := podsapi.CreateContainerAndPodTemplate("")
		selectorLabels := map[string]string{
			"workload.user.cattle.io/workloadselector": fmt.Sprintf("apps.deployment-%v-%v", namespaceName, deploymentName),
		}
		if podTemplate.ObjectMeta.Labels == nil {
			podTemplate.ObjectMeta.Labels = make(map[string]string)
		}
		for k, v := range selectorLabels {
			podTemplate.ObjectMeta.Labels[k] = v
		}
		replicas := int32(1)
		deployment := &appsv1.Deployment{
			ObjectMeta: metav1.ObjectMeta{
				Name:      deploymentName,
				Namespace: namespaceName,
			},
			Spec: appsv1.DeploymentSpec{
				Replicas: &replicas,
				Selector: &metav1.LabelSelector{
					MatchLabels: selectorLabels,
				},
				Template: podTemplate,
			},
		}
		_, err := userContext.Apps.Deployment().Create(deployment)
		return err == nil, err
	case DeleteVerb:
		err := userContext.Apps.Deployment().Delete(namespaceName, deploymentName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'deployments'", verb)
	}
}

// CheckSecretAccess checks if a user has the specified access to a secret in a namespace. It returns true if the user has access, false otherwise.
func CheckSecretAccess(userContext *wrangler.Context, verb, namespaceName, secretName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Core.Secret().Get(namespaceName, secretName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Core.Secret().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case DeleteVerb:
		err := userContext.Core.Secret().Delete(namespaceName, secretName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'secrets'", verb)
	}
}

// CheckPrtbAccess checks if a user has the specified access to a project role template binding in a namespace. It returns true if the user has access, false otherwise.
func CheckPrtbAccess(userContext *wrangler.Context, verb, prtbNamespace, prtbName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbNamespace, prtbName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().List(prtbNamespace, metav1.ListOptions{})
		return err == nil, err
	case DeleteVerb:
		err := userContext.Mgmt.ProjectRoleTemplateBinding().Delete(prtbNamespace, prtbName, &metav1.DeleteOptions{})
		return err == nil, err
	case UpdateVerb:
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
	case PatchVerb:
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
	case GetVerb:
		_, err := userContext.Core.ConfigMap().Get(namespaceName, configMapName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Core.ConfigMap().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case DeleteVerb:
		err := userContext.Core.ConfigMap().Delete(namespaceName, configMapName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'configmaps'", verb)
	}
}

// CheckStatefulSetAccess checks if a user has the specified access to a StatefulSet in a namespace. It returns true if the user has access, false otherwise.
func CheckStatefulSetAccess(userContext *wrangler.Context, verb, namespaceName, statefulSetName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Apps.StatefulSet().Get(namespaceName, statefulSetName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Apps.StatefulSet().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case DeleteVerb:
		err := userContext.Apps.StatefulSet().Delete(namespaceName, statefulSetName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'statefulsets'", verb)
	}
}

// CheckNodeAccess checks if a user has the specified access to a Node. It returns true if the user has access, false otherwise.
func CheckNodeAccess(userContext *wrangler.Context, verb, resourceName string) (bool, error) {
	switch verb {
	case GetVerb:
		_, err := userContext.Core.Node().Get(resourceName, metav1.GetOptions{})
		return err == nil, err
	case ListVerb:
		_, err := userContext.Core.Node().List(metav1.ListOptions{})
		return err == nil, err
	case DeleteVerb:
		err := userContext.Core.Node().Delete(resourceName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'nodes'", verb)
	}
}

// VerifyBindingsForCrtb verifies RoleBindings and ClusterRoleBindings for a given CRTB
func VerifyBindingsForCrtb(client *rancher.Client, clusterID string, crtb *v3.ClusterRoleTemplateBinding, expectedRoleBindingCount, expectedClusterRoleBindingCount int) error {
	return verifyBindings(client, clusterID, crtb.UserName, crtb.RoleTemplateName, crtb.Name, []string{crtb.Namespace}, expectedRoleBindingCount, expectedClusterRoleBindingCount)
}

// VerifyBindingsForPrtb verifies binding counts and role references, optionally using explicit expected ClusterRole names.
func VerifyBindingsForPrtb(client *rancher.Client, clusterID string, prtb *v3.ProjectRoleTemplateBinding, namespaces []*corev1.Namespace, expectedRoleBindingCount, expectedClusterRoleBindingCount int, expectedClusterRoleNames ...string) error {
	namespaceNames := []string{}
	defaultNamespace := strings.SplitN(prtb.ProjectName, ":", 2)[0]

	if len(namespaces) == 0 {
		namespaceNames = append(namespaceNames, defaultNamespace)
	} else {
		for _, ns := range namespaces {
			namespaceNames = append(namespaceNames, ns.Name)
		}
	}

	return verifyBindings(client, clusterID, prtb.UserName, prtb.RoleTemplateName, prtb.Name, namespaceNames, expectedRoleBindingCount, expectedClusterRoleBindingCount, expectedClusterRoleNames...)
}

// VerifyMainACRContainsAllRules verifies that the main ACR contains all rules from the main role template and its child role templates
func VerifyMainACRContainsAllRules(client *rancher.Client, clusterID, mainRTName string, childRTNames []string) error {
	acrNameRegular := mainRTName + ResourceAggregator

	var lastErr error
	pollErr := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		mainRules, err := GetClusterRoleRules(client, clusterID, mainRTName)
		if err != nil {
			lastErr = fmt.Errorf("failed to get mainRole rules: %w", err)
			return false, nil
		}

		var allChildRules []rbacv1.PolicyRule
		for _, childRTName := range childRTNames {
			childRules, err := GetClusterRoleRules(client, clusterID, childRTName)
			if err != nil {
				lastErr = fmt.Errorf("failed to get childRole rules %s: %w", childRTName, err)
				return false, nil
			}
			allChildRules = append(allChildRules, childRules...)
		}

		expectedRules := append(mainRules, allChildRules...)

		actualRulesRegular, err := GetClusterRoleRules(client, clusterID, acrNameRegular)
		if err != nil {
			lastErr = fmt.Errorf("failed to get ACR %s: %w", acrNameRegular, err)
			return false, nil
		}

		if !ruleSlicesMatch(actualRulesRegular, expectedRules) {
			lastErr = fmt.Errorf("ACR %s rules do not match expected combined rules", acrNameRegular)
			return false, nil
		}

		return true, nil
	})

	if pollErr != nil {
		if lastErr != nil {
			return lastErr
		}
		return pollErr
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
	var lastErr error
	pollErr := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		mainRules, err := GetClusterRoleRules(client, clusterID, mainRTName)
		if err != nil {
			lastErr = err
			return false, nil
		}

		allChildRules := []rbacv1.PolicyRule{}
		for _, childRTName := range childRTNames {
			childRules, err := GetClusterRoleRules(client, clusterID, childRTName)
			if err != nil {
				lastErr = err
				return false, nil
			}
			allChildRules = append(allChildRules, childRules...)
		}

		expectedRules := append(mainRules, allChildRules...)
		mgmtRules := filterMgmtRules(expectedRules, managementContext)

		acrRules, err := GetClusterRoleRules(client, clusterID, acrName)
		if err != nil {
			lastErr = fmt.Errorf("failed to get ACR %s: %w", acrName, err)
			return false, nil
		}

		if !ruleSlicesMatch(acrRules, mgmtRules) {
			lastErr = fmt.Errorf("ACR %s rules do not match expected combined rules.\nExpected: %+v\nActual: %+v", acrName, mgmtRules, acrRules)
			return false, nil
		}

		return true, nil
	})

	if pollErr != nil {
		if lastErr != nil {
			return lastErr
		}
		return pollErr
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

func verifyBindings(client *rancher.Client, clusterID, userName, roleTemplateName, roleTemplateBindingName string, namespaces []string, expectedRBCount, expectedCRBCount int, expectedClusterRoleNames ...string) error {
	var lastErr error
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.TwoMinuteTimeout, false, func(context.Context) (bool, error) {
		for _, ns := range namespaces {
			rbs, err := extrbacapi.ListRoleBindings(client, clusterID, ns, metav1.ListOptions{})
			if err != nil {
				lastErr = fmt.Errorf("failed to list RoleBindings in namespace %s: %w", ns, err)
				return false, nil
			}

			filtered := filterRoleBindings(&rbacv1.RoleBindingList{Items: rbs.Items}, userName, roleTemplateName)
			if len(filtered) != expectedRBCount {
				lastErr = fmt.Errorf("expected %d RoleBindings for user %s in namespace %s, got %d",
					expectedRBCount, userName, ns, len(filtered))
				return false, nil
			}

			if expectedRBCount > 0 {
				expected := expectedRoleNames(clusterID, roleTemplateBindingName, roleTemplateName, expectedRBCount)
				expectedSet := make(map[string]struct{}, len(expected))
				for _, name := range expected {
					expectedSet[name] = struct{}{}
				}

				for _, rb := range filtered {
					if _, ok := expectedSet[rb.RoleRef.Name]; !ok {
						lastErr = fmt.Errorf("unexpected RoleBinding RoleRef.Name %s, expected %v",
							rb.RoleRef.Name, expected)
						return false, nil
					}
				}
			}
		}

		crbs, err := extrbacapi.ListClusterRoleBindings(client, clusterID, metav1.ListOptions{})
		if err != nil {
			lastErr = fmt.Errorf("failed to list ClusterRoleBindings: %w", err)
			return false, nil
		}

		filteredCRBs := filterClusterRoleBindings(&rbacv1.ClusterRoleBindingList{Items: crbs.Items}, userName, roleTemplateName)
		if len(filteredCRBs) != expectedCRBCount {
			lastErr = fmt.Errorf("expected %d ClusterRoleBindings, got %d",
				expectedCRBCount, len(filteredCRBs))
			return false, nil
		}

		if expectedCRBCount > 0 {
			expected := expectedRoleNames(clusterID, roleTemplateBindingName, roleTemplateName, expectedCRBCount)
			if len(expectedClusterRoleNames) > 0 {
				expected = expectedClusterRoleNames
			}
			expectedSet := make(map[string]struct{}, len(expected))
			for _, name := range expected {
				expectedSet[name] = struct{}{}
			}

			for _, crb := range filteredCRBs {
				if _, ok := expectedSet[crb.RoleRef.Name]; !ok {
					lastErr = fmt.Errorf("unexpected ClusterRoleBinding RoleRef.Name %s, expected %v",
						crb.RoleRef.Name, expected)
					return false, nil
				}
			}
		}

		lastErr = nil
		return true, nil
	})
	if err != nil {
		return lastErr
	}

	return nil
}

func expectedRoleNames(clusterID, bindingName, rtName string, count int) []string {
	if clusterID != extclusterapi.LocalCluster {
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

// VerifyBindingsDeletedForProvider waits until no global, cluster or project role binding still references a principal emitted by the given auth provider
func VerifyBindingsDeletedForProvider(client *rancher.Client, providerName string) error {
	prefix := providerName + "_"

	var lastErr error

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(context.Context) (bool, error) {
		remaining, checkErr := remainingBindingsForProvider(client, prefix)
		if checkErr != nil {
			lastErr = checkErr
			return false, nil
		}

		if len(remaining) > 0 {
			lastErr = fmt.Errorf("bindings still reference %s principals: %s", providerName, strings.Join(remaining, ", "))
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return fmt.Errorf("role bindings were not cleaned up: %w", lastErr)
	}

	return nil
}

func remainingBindingsForProvider(client *rancher.Client, prefix string) ([]string, error) {
	var remaining []string

	grbs, err := client.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list global role bindings: %w", err)
	}

	for _, grb := range grbs.Items {
		if strings.HasPrefix(grb.GroupPrincipalName, prefix) || strings.HasPrefix(grb.UserPrincipalName, prefix) {
			remaining = append(remaining, fmt.Sprintf(
				"globalrolebinding/%s (userPrincipal %q, groupPrincipal %q, created %s)",
				grb.Name,
				grb.UserPrincipalName,
				grb.GroupPrincipalName,
				grb.CreationTimestamp.UTC().Format(time.RFC3339),
			))
		}
	}

	crtbs, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().List(allNamespaces, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role template bindings: %w", err)
	}

	for _, crtb := range crtbs.Items {
		if strings.HasPrefix(crtb.GroupPrincipalName, prefix) || strings.HasPrefix(crtb.UserPrincipalName, prefix) {
			remaining = append(remaining, fmt.Sprintf(
				"clusterroletemplatebinding/%s/%s (userPrincipal %q, groupPrincipal %q, created %s)",
				crtb.Namespace,
				crtb.Name,
				crtb.UserPrincipalName,
				crtb.GroupPrincipalName,
				crtb.CreationTimestamp.UTC().Format(time.RFC3339),
			))
		}
	}

	prtbs, err := client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().List(allNamespaces, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list project role template bindings: %w", err)
	}

	for _, prtb := range prtbs.Items {
		if strings.HasPrefix(prtb.GroupPrincipalName, prefix) || strings.HasPrefix(prtb.UserPrincipalName, prefix) {
			remaining = append(remaining, fmt.Sprintf(
				"projectroletemplatebinding/%s/%s (userPrincipal %q, groupPrincipal %q, created %s)",
				prtb.Namespace,
				prtb.Name,
				prtb.UserPrincipalName,
				prtb.GroupPrincipalName,
				prtb.CreationTimestamp.UTC().Format(time.RFC3339),
			))
		}
	}

	return remaining, nil
}

// VerifyUserCanI validates authorization without performing the requested resource operation.
func VerifyUserCanI(userClient *rancher.Client, clusterID, verb, apiGroup, resourceType, namespaceName, resourceName string, expected bool) error {
	userContext, err := extclusterapi.GetClusterWranglerContext(userClient, clusterID)
	if err != nil {
		return err
	}

	authorization, err := authzclient.NewForConfig(userContext.RESTConfig)
	if err != nil {
		return err
	}

	var allowed bool
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		review, err := authorization.SelfSubjectAccessReviews().Create(
			ctx,
			&authzv1.SelfSubjectAccessReview{
				Spec: authzv1.SelfSubjectAccessReviewSpec{
					ResourceAttributes: &authzv1.ResourceAttributes{
						Verb:      verb,
						Group:     apiGroup,
						Resource:  resourceType,
						Namespace: namespaceName,
						Name:      resourceName,
					},
				},
			},
			metav1.CreateOptions{},
		)
		if err != nil {
			return false, err
		}

		if review.Status.EvaluationError != "" {
			return false, fmt.Errorf("access review evaluation failed: %s", review.Status.EvaluationError)
		}

		allowed = review.Status.Allowed
		return allowed == expected, nil
	})

	if err != nil {
		return fmt.Errorf("permission %s %s/%s namespace=%q name=%q: allowed=%t, expected=%t: %w", verb, apiGroup, resourceType, namespaceName, resourceName, allowed, expected, err)
	}

	return nil
}

// VerifyPolicyRules compares declared rules regardless of ordering or duplicate rules; exact also rejects extra rules.
func VerifyPolicyRules(actual, expected []rbacv1.PolicyRule, exact bool) error {
	actual = slices.Clone(actual)
	expected = slices.Clone(expected)
	for _, rules := range [][]rbacv1.PolicyRule{actual, expected} {
		for index := range rules {
			rules[index] = *rules[index].DeepCopy()
			for _, values := range [][]string{rules[index].Verbs, rules[index].APIGroups, rules[index].Resources, rules[index].ResourceNames, rules[index].NonResourceURLs} {
				slices.Sort(values)
			}
		}
	}
	for _, expectedRule := range expected {
		if !slices.ContainsFunc(actual, func(actualRule rbacv1.PolicyRule) bool {
			return ruleSlicesMatch([]rbacv1.PolicyRule{actualRule}, []rbacv1.PolicyRule{expectedRule})
		}) {
			return fmt.Errorf("expected policy rule not found: %+v", expectedRule)
		}
	}
	if exact {
		for _, actualRule := range actual {
			if !slices.ContainsFunc(expected, func(expectedRule rbacv1.PolicyRule) bool {
				return ruleSlicesMatch([]rbacv1.PolicyRule{actualRule}, []rbacv1.PolicyRule{expectedRule})
			}) {
				return fmt.Errorf("unexpected policy rule: %+v", actualRule)
			}
		}
	}
	return nil
}

// VerifyExactClusterRoleRules waits for a ClusterRole by exact name to contain all expected rules and no extra rules.
func VerifyExactClusterRoleRules(client *rancher.Client, clusterID, name string, expected []rbacv1.PolicyRule) (*rbacv1.ClusterRole, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get cluster context for %s: %w", clusterID, err)
	}

	var clusterrole *rbacv1.ClusterRole
	var lastErr error
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		var getErr error
		clusterrole, getErr = clusterContext.RBAC.ClusterRole().Get(name, metav1.GetOptions{})
		if getErr != nil {
			lastErr = getErr
			if apierrors.IsNotFound(getErr) {
				return false, nil
			}
			return false, getErr
		}

		lastErr = VerifyPolicyRules(clusterrole.Rules, expected, true)
		return lastErr == nil, nil
	})
	if err != nil {
		return nil, fmt.Errorf("ClusterRole %s in cluster %s did not match expected rules: %w (last observation: %v)", name, clusterID, err, lastErr)
	}

	return clusterrole, nil
}

// VerifyRoleTemplateClusterRoleRules waits for generated ClusterRoles to contain all expected rules, allowing extra rules.
func VerifyRoleTemplateClusterRoleRules(client *rancher.Client, clusterID, templateName string, expectedRules []rbacv1.PolicyRule) error {
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		roles, err := GetClusterRolesForRoleTemplates(client, clusterID, templateName)
		if err != nil {
			return false, err
		}

		var actualRules []rbacv1.PolicyRule
		for _, role := range roles.Items {
			actualRules = append(actualRules, role.Rules...)
		}
		return len(roles.Items) > 0 && VerifyPolicyRules(actualRules, expectedRules, false) == nil, nil
	})

	if err != nil {
		return fmt.Errorf("ClusterRole rules for RoleTemplate %s in cluster %s: %w", templateName, clusterID, err)
	}

	return nil
}

// VerifyClusterScopedRoleTemplateRBAC waits for a user's template bindings to grant exactly the expected cluster-scoped rules.
func VerifyClusterScopedRoleTemplateRBAC(client *rancher.Client, clusterID, templateName, userName string, expected []rbacv1.PolicyRule) ([]string, []string, error) {
	var roleNames, bindingNames []string
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		roles, err := GetClusterRolesForRoleTemplates(client, clusterID, templateName)
		if err != nil {
			return false, err
		}

		bindings, err := extrbacapi.ListClusterRoleBindings(client, clusterID, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		roleNames, bindingNames = nil, nil
		var effectiveRules []rbacv1.PolicyRule
		for _, binding := range bindings.Items {
			if !slices.ContainsFunc(binding.Subjects, func(subject rbacv1.Subject) bool {
				return subject.Kind == rbacv1.UserKind && subject.Name == userName
			}) {
				continue
			}
			foundRole := false
			for _, role := range roles.Items {
				if binding.RoleRef.Kind == "ClusterRole" && binding.RoleRef.Name == role.Name {
					foundRole = true
					bindingNames = append(bindingNames, binding.Name)
					if !slices.Contains(roleNames, role.Name) {
						roleNames = append(roleNames, role.Name)
						effectiveRules = append(effectiveRules, role.Rules...)
					}
				}
			}
			if binding.RoleRef.Kind == "ClusterRole" && strings.HasPrefix(binding.RoleRef.Name, templateName) && !foundRole {
				return false, nil
			}
		}

		if len(expected) == 0 {
			return len(bindingNames) == 0, nil
		}

		return len(bindingNames) > 0 && VerifyPolicyRules(effectiveRules, expected, true) == nil, nil
	})

	if err != nil {
		return nil, nil, fmt.Errorf("cluster-scoped RBAC for %s and user %s: %w", templateName, userName, err)
	}

	return roleNames, bindingNames, nil
}

// VerifyRoleTemplateResourceRules waits for resource rules with non-empty verbs to be present or absent in a template's ClusterRoles.
func VerifyRoleTemplateResourceRules(client *rancher.Client, clusterID, templateName, apiGroup, resource string, expectedPresent bool) error {
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		roles, err := GetClusterRolesForRoleTemplates(client, clusterID, templateName)
		if err != nil {
			return false, err
		}
		for _, role := range roles.Items {
			for _, rule := range role.Rules {
				if (slices.Contains(rule.APIGroups, apiGroup) || slices.Contains(rule.APIGroups, Wildcard)) && (slices.Contains(rule.Resources, resource) || slices.Contains(rule.Resources, Wildcard)) && len(rule.Verbs) > 0 {
					return expectedPresent, nil
				}
			}
		}
		return !expectedPresent, nil
	})
	if err != nil {
		return fmt.Errorf("RoleTemplate %s resource %s/%s expected present=%t: %w", templateName, apiGroup, resource, expectedPresent, err)
	}
	return nil
}

// VerifyUserNamespaceRuleRestriction verifies namespace get grants include the allowed namespace without unrestricted or denied namespace access.
func VerifyUserNamespaceRuleRestriction(client *rancher.Client, clusterID, userName, allowedNamespace, deniedNamespace string) error {
	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		bindings, err := extrbacapi.ListClusterRoleBindings(client, clusterID, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		foundScopedGrant := false
		for _, binding := range bindings.Items {
			if !slices.ContainsFunc(binding.Subjects, func(subject rbacv1.Subject) bool {
				return subject.Kind == rbacv1.UserKind && subject.Name == userName
			}) {
				continue
			}
			rules, err := GetClusterRoleRules(client, clusterID, binding.RoleRef.Name)
			if err != nil {
				return false, err
			}
			for _, rule := range rules {
				if (slices.Contains(rule.APIGroups, "") || slices.Contains(rule.APIGroups, Wildcard)) && (slices.Contains(rule.Resources, NamespaceResource) || slices.Contains(rule.Resources, Wildcard)) && (slices.Contains(rule.Verbs, GetVerb) || slices.Contains(rule.Verbs, Wildcard)) {
					if len(rule.ResourceNames) == 0 || slices.Contains(rule.ResourceNames, deniedNamespace) {
						return false, fmt.Errorf("namespace access unexpectedly unrestricted or includes %s in %s", deniedNamespace, binding.RoleRef.Name)
					}
					foundScopedGrant = foundScopedGrant || slices.Contains(rule.ResourceNames, allowedNamespace)
				}
			}
		}
		return foundScopedGrant, nil
	})
	if err != nil {
		return fmt.Errorf("namespace rule restriction for user %s: %w", userName, err)
	}

	return nil
}

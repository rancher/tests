package rbac

import (
	"context"
	"fmt"
	"strings"

	"github.com/rancher/norman/types"
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	"github.com/sirupsen/logrus"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/selection"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	ManagementAPIGroup                 = "management.cattle.io"
	Version                            = "v3"
	ResourceAggregator                 = "-aggregator"
	ClusterMgmtResourceAggregator      = "-cluster-mgmt-aggregator"
	ProjectMgmtResourceAggregator      = "-project-mgmt-aggregator"
	ClusterMgmtResource                = "-cluster-mgmt"
	ProjectMgmtResource                = "-project-mgmt"
	AggregatedRoleTemplatesFeatureFlag = "aggregated-roletemplates"
	ClusterContext                     = "cluster"
	ProjectContext                     = "project"
	RkeCattleAPIGroup                  = "rke.cattle.io"
	ProjectCattleAPIGroup              = "project.cattle.io"
	AppsAPIGroup                       = "apps"
	CompletedSummary                   = "Completed"
	MembershipBindingOwnerLabel        = "membership-binding-owner"
	UsersResource                      = "users"
	UserAttributeResource              = "userattribute"
	GroupsResource                     = "groups"
	GroupMembersResource               = "groupmembers"
	ProjectResource                    = "projects"
	PrtbResource                       = "projectroletemplatebindings"
	SecretsResource                    = "secrets"
	DeploymentsResource                = "deployments"
	PodsResource                       = "pods"
	ManageUsersVerb                    = "manage-users"
	UpdatePsaVerb                      = "updatepsa"
	ClusterOwnerRoleSuffix             = "clusterowner"
	ClusterMemberRoleSuffix            = "clustermember"
	ProjectOwnerRoleSuffix             = "projectowner"
	ProjectMemberRoleSuffix            = "projectmember"
)

var (
	ClusterMgmtResources = map[string]string{
		"clusterscans":                ManagementAPIGroup,
		"clusterregistrationtokens":   ManagementAPIGroup,
		"clusterroletemplatebindings": ManagementAPIGroup,
		"etcdbackups":                 ManagementAPIGroup,
		"nodes":                       ManagementAPIGroup,
		"nodepools":                   ManagementAPIGroup,
		"projects":                    ManagementAPIGroup,
		"etcdsnapshots":               RkeCattleAPIGroup,
	}

	ProjectMgmtResources = map[string]string{
		"sourcecodeproviderconfigs":   ProjectCattleAPIGroup,
		"projectroletemplatebindings": ManagementAPIGroup,
		"secrets":                     "",
	}

	PolicyRules = map[string][]rbacv1.PolicyRule{
		"readProjects":    definePolicyRules([]string{"get", "list"}, []string{"projects"}, []string{ManagementAPIGroup}),
		"createProjects":  definePolicyRules([]string{"create"}, []string{"projects"}, []string{ManagementAPIGroup}),
		"updateProjects":  definePolicyRules([]string{"update", "patch"}, []string{"projects"}, []string{ManagementAPIGroup}),
		"deleteProjects":  definePolicyRules([]string{"delete"}, []string{"projects"}, []string{ManagementAPIGroup}),
		"manageProjects":  definePolicyRules([]string{"create", "update", "patch", "delete"}, []string{"projects"}, []string{ManagementAPIGroup}),
		"readPrtbs":       definePolicyRules([]string{"get", "list"}, []string{"projectroletemplatebindings"}, []string{ManagementAPIGroup}),
		"updatePrtbs":     definePolicyRules([]string{"update", "patch"}, []string{"projectroletemplatebindings"}, []string{ManagementAPIGroup}),
		"readDeployments": definePolicyRules([]string{"get", "list"}, []string{"deployments"}, []string{AppsAPIGroup}),
		"readPods":        definePolicyRules([]string{"get", "list"}, []string{"pods"}, []string{""}),
		"readNamespaces":  definePolicyRules([]string{"get", "list"}, []string{"namespaces"}, []string{""}),
		"readSecrets":     definePolicyRules([]string{"get", "list"}, []string{"secrets"}, []string{""}),
	}
)

// RoleGroupVersionResource is the required Group Version Resource for accessing roles in a cluster, using the dynamic client.
var RoleGroupVersionResource = schema.GroupVersionResource{
	Group:    rbacv1.SchemeGroupVersion.Group,
	Version:  rbacv1.SchemeGroupVersion.Version,
	Resource: "roles",
}

// ClusterRoleGroupVersionResource is the required Group Version Resource for accessing clusterroles in a cluster, using the dynamic client.
var ClusterRoleGroupVersionResource = schema.GroupVersionResource{
	Group:    rbacv1.SchemeGroupVersion.Group,
	Version:  rbacv1.SchemeGroupVersion.Version,
	Resource: "clusterroles",
}

// RoleBindingGroupVersionResource is the required Group Version Resource for accessing rolebindings in a cluster, using the dynamic client.
var RoleBindingGroupVersionResource = schema.GroupVersionResource{
	Group:    rbacv1.SchemeGroupVersion.Group,
	Version:  rbacv1.SchemeGroupVersion.Version,
	Resource: "rolebindings",
}

// ClusterRoleBindingGroupVersionResource is the required Group Version Resource for accessing clusterrolebindings in a cluster, using the dynamic client.
var ClusterRoleBindingGroupVersionResource = schema.GroupVersionResource{
	Group:    rbacv1.SchemeGroupVersion.Group,
	Version:  rbacv1.SchemeGroupVersion.Version,
	Resource: "clusterrolebindings",
}

// GlobalRoleGroupVersionResource is the required Group Version Resource for accessing global roles in a rancher server, using the dynamic client.
var GlobalRoleGroupVersionResource = schema.GroupVersionResource{
	Group:    ManagementAPIGroup,
	Version:  Version,
	Resource: "globalroles",
}

// GlobalRoleBindingGroupVersionResource is the required Group Version Resource for accessing clusterrolebindings in a cluster, using the dynamic client.
var GlobalRoleBindingGroupVersionResource = schema.GroupVersionResource{
	Group:    ManagementAPIGroup,
	Version:  Version,
	Resource: "globalrolebindings",
}

// ClusterRoleTemplateBindingGroupVersionResource is the required Group Version Resource for accessing clusterrolebindings in a cluster, using the dynamic client.
var ClusterRoleTemplateBindingGroupVersionResource = schema.GroupVersionResource{
	Group:    ManagementAPIGroup,
	Version:  Version,
	Resource: "clusterroletemplatebindings",
}

// RoleTemplateGroupVersionResource is the required Group Version Resource for accessing roletemplates in a cluster, using the dynamic client.
var RoleTemplateGroupVersionResource = schema.GroupVersionResource{
	Group:    ManagementAPIGroup,
	Version:  Version,
	Resource: "roletemplates",
}

// ProjectRoleTemplateBindingGroupVersionResource is the required Group Version Resource for accessing projectroletemplatebindings in a cluster, using the dynamic client.
var ProjectRoleTemplateBindingGroupVersionResource = schema.GroupVersionResource{
	Group:    ManagementAPIGroup,
	Version:  Version,
	Resource: "projectroletemplatebindings",
}

// GetGlobalRoleBindingByName is a helper function to fetch global role binding by name
func GetGlobalRoleBindingByName(client *rancher.Client, globalRoleBindingName string) (*v3.GlobalRoleBinding, error) {
	var matchingGRB *v3.GlobalRoleBinding

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		var getErr error
		matchingGRB, getErr = client.WranglerContext.Mgmt.GlobalRoleBinding().Get(globalRoleBindingName, metav1.GetOptions{})
		if getErr != nil {
			return false, nil
		}

		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("error while polling for global role binding %s: %w", globalRoleBindingName, err)
	}

	return matchingGRB, nil
}

// GetGlobalRoleByName is a helper function to fetch global role by name
func GetGlobalRoleByName(client *rancher.Client, globalRoleName string) (*v3.GlobalRole, error) {
	return client.WranglerContext.Mgmt.GlobalRole().Get(globalRoleName, metav1.GetOptions{})
}

// GetRoleTemplateByName is a helper function to fetch role template by name using wrangler context
func GetRoleTemplateByName(client *rancher.Client, roleTemplateName string) (*v3.RoleTemplate, error) {
	var roleTemplate *v3.RoleTemplate

	err := kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.TenSecondTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		rt, err := client.WranglerContext.Mgmt.RoleTemplate().Get(roleTemplateName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}
			return false, err
		}

		roleTemplate = rt
		return true, nil
	})

	if err != nil {
		return nil, fmt.Errorf("error while polling for role template: %w", err)
	}

	return roleTemplate, nil
}

// GetRoleTemplateContext is a helper function to fetch the context of a role template
func GetRoleTemplateContext(client *rancher.Client, roleTemplateName string) (string, error) {
	roleTemplate, err := GetRoleTemplateByName(client, roleTemplateName)
	if err != nil {
		return "", fmt.Errorf("failed to get RoleTemplate %s: %w", roleTemplateName, err)
	}

	if roleTemplate == nil {
		return "", fmt.Errorf("RoleTemplate %s not found", roleTemplateName)
	}

	return roleTemplate.Context, nil
}

// GetClusterRolesForRoleTemplates gets ClusterRoles associated with the provided role templates
func GetClusterRolesForRoleTemplates(client *rancher.Client, clusterID string, rtNames ...string) (*rbacv1.ClusterRoleList, error) {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	allClusterRoles, err := ctx.RBAC.ClusterRole().List(metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var filtered rbacv1.ClusterRoleList
	seen := map[string]bool{}

	for _, cr := range allClusterRoles.Items {
		for _, rtName := range rtNames {
			if strings.HasPrefix(cr.Name, rtName) && !seen[cr.Name] {
				filtered.Items = append(filtered.Items, cr)
				seen[cr.Name] = true
				break
			}
		}
	}

	return &filtered, nil
}

// GetClusterRoleRules is a helper function to fetch rules for a cluster role
func GetClusterRoleRules(client *rancher.Client, clusterID string, clusterRoleName string) ([]rbacv1.PolicyRule, error) {
	ctx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	clusterRole, err := ctx.RBAC.ClusterRole().Get(clusterRoleName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			return nil, fmt.Errorf("ClusterRole %s not found", clusterRoleName)
		}
		return nil, fmt.Errorf("failed to get ClusterRole %s: %w", clusterRoleName, err)
	}

	return clusterRole.Rules, nil
}

// GetClusterRoleTemplateBindingsForGroup polls until a CRTB for the given group principal and cluster is found.
func GetClusterRoleTemplateBindingsForGroup(rancherClient *rancher.Client, groupPrincipalName, clusterID string) (*v3.ClusterRoleTemplateBinding, error) {
	var matchingCRTB *v3.ClusterRoleTemplateBinding
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		crtbList, err := ListClusterRoleTemplateBindings(rancherClient, metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		for _, crtb := range crtbList.Items {
			if crtb.GroupPrincipalName == groupPrincipalName && crtb.ClusterName == clusterID {
				matchingCRTB = &crtb
				return true, nil
			}
		}
		return false, nil
	})
	if err != nil {
		return nil, fmt.Errorf("error while polling for group crtb: %w", err)
	}
	return matchingCRTB, nil
}

// WaitForCrtbStatus waits for the CRTB to reach the Completed status or checks for its existence if status field is not supported (older Rancher versions)
func WaitForCrtbStatus(client *rancher.Client, crtbNamespace, crtbName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaults.OneMinuteTimeout)
	defer cancel()

	err := kwait.PollUntilContextTimeout(ctx, defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		crtb, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Get(crtbNamespace, crtbName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		if crtb.Status.Summary == CompletedSummary {
			return true, nil
		}

		if crtb != nil && crtb.Name == crtbName && crtb.Namespace == crtbNamespace {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("timed out waiting for CRTB %s/%s to complete or exist: %w", crtbNamespace, crtbName, err)
	}

	return nil
}

// WaitForPrtbExistence waits for the PRTB to exist with the correct user and project
func WaitForPrtbExistence(client *rancher.Client, project *v3.Project, prtbObj *v3.ProjectRoleTemplateBinding, user *management.User) (*v3.ProjectRoleTemplateBinding, error) {
	projectName := fmt.Sprintf("%s:%s", project.Namespace, project.Name)

	var prtb *v3.ProjectRoleTemplateBinding
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		var err error
		prtb, err = client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbObj.Namespace, prtbObj.Name, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		if prtb != nil && prtb.UserName == user.ID && prtb.ProjectName == projectName {
			return true, nil
		}

		return false, nil
	})
	if err != nil {
		return nil, err
	}
	return prtb, nil
}

// GetRoleBindingsForUsers gets RoleBindings where users are subjects in specific namespaces
func GetRoleBindingsForUsers(client *rancher.Client, userName string, namespaces []string) ([]rbacv1.RoleBinding, error) {
	var userRBs []rbacv1.RoleBinding

	for _, namespace := range namespaces {
		rbs, err := ListRoleBindings(client, clusterapi.LocalCluster, namespace, metav1.ListOptions{})
		if err != nil {
			return nil, fmt.Errorf("failed to list RoleBindings in namespace %s: %w", namespace, err)
		}

		for _, rb := range rbs.Items {
			for _, subject := range rb.Subjects {
				if subject.Kind == "User" && subject.Name == userName {
					userRBs = append(userRBs, rb)
				}
			}
		}
	}

	return userRBs, nil
}

func definePolicyRules(verbs, resources, apiGroups []string) []rbacv1.PolicyRule {
	return []rbacv1.PolicyRule{{
		Verbs:     verbs,
		Resources: resources,
		APIGroups: apiGroups,
	}}
}

// GetRoleBindings is a helper function to fetch rolebindings for a user
func GetRoleBindings(rancherClient *rancher.Client, clusterID string, userID string) ([]rbacv1.RoleBinding, error) {
	logrus.Infof("Getting role bindings for user %s in cluster %s", userID, clusterID)
	listOpt := metav1.ListOptions{}
	roleBindings, err := ListRoleBindings(rancherClient, clusterID, "", listOpt)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch RoleBindings: %w", err)
	}

	var userRoleBindings []rbacv1.RoleBinding
	for _, rb := range roleBindings.Items {
		for _, subject := range rb.Subjects {
			if subject.Name == userID {
				userRoleBindings = append(userRoleBindings, rb)
				break
			}
		}
	}
	logrus.Infof("Found %d role bindings for user %s", len(userRoleBindings), userID)
	return userRoleBindings, nil
}

// GetBindings is a helper function to fetch bindings for a user
func GetBindings(rancherClient *rancher.Client, userID string) (map[string]interface{}, error) {
	bindings := make(map[string]interface{})

	roleBindings, err := GetRoleBindings(rancherClient, clusterapi.LocalCluster, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get role bindings: %w", err)
	}
	bindings["RoleBindings"] = roleBindings

	clusterRoleBindings, err := ListClusterRoleBindings(rancherClient, clusterapi.LocalCluster, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role bindings: %w", err)
	}
	bindings["ClusterRoleBindings"] = clusterRoleBindings.Items

	globalRoleBindings, err := rancherClient.Management.GlobalRoleBinding.ListAll(&types.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to list global role bindings: %w", err)
	}
	bindings["GlobalRoleBindings"] = globalRoleBindings.Data

	clusterRoleTemplateBindings, err := rancherClient.Management.ClusterRoleTemplateBinding.List(&types.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role template bindings: %w", err)
	}
	bindings["ClusterRoleTemplateBindings"] = clusterRoleTemplateBindings.Data

	return bindings, nil
}

// GetGlobalRoleBindingByUserAndRole is a helper function to fetch global role binding for a user associated with a specific global role
func GetGlobalRoleBindingByUserAndRole(client *rancher.Client, userID, globalRoleName string) (*v3.GlobalRoleBinding, error) {
	var matchingGlobalRoleBinding *v3.GlobalRoleBinding

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.TenSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		grblist, err := client.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		for _, grb := range grblist.Items {
			if grb.GlobalRoleName == globalRoleName && grb.UserName == userID {
				matchingGlobalRoleBinding = &grb
				return true, nil
			}
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("error while polling for global role binding: %w", err)
	}

	return matchingGlobalRoleBinding, nil
}

// GetClusterRoleTemplateBindingsForUser fetches clusterroletemplatebindings for a specific user
func GetClusterRoleTemplateBindingsForUser(rancherClient *rancher.Client, userID string) (*v3.ClusterRoleTemplateBinding, error) {
	var matchingCRTB *v3.ClusterRoleTemplateBinding
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		crtbList, err := ListClusterRoleTemplateBindings(rancherClient, metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		for _, crtb := range crtbList.Items {
			if crtb.UserName == userID {
				matchingCRTB = &crtb
				return true, nil
			}
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("error while polling for crtb: %w", err)
	}

	return matchingCRTB, nil
}

// ListCRTBsByLabel lists ClusterRoleTemplateBindings by label selector
func ListCRTBsByLabel(client *rancher.Client, labelKey, labelValue string, expectedCount int) (*v3.ClusterRoleTemplateBindingList, error) {
	req, err := labels.NewRequirement(labelKey, selection.In, []string{labelValue})
	if err != nil {
		return nil, err
	}

	selector := labels.NewSelector().Add(*req)
	var crtbs *v3.ClusterRoleTemplateBindingList

	ctx, cancel := context.WithTimeout(context.Background(), defaults.TwoMinuteTimeout)
	defer cancel()

	err = kwait.PollUntilContextTimeout(ctx, defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		crtbs, pollErr = ListClusterRoleTemplateBindings(client, metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if pollErr != nil {
			return false, pollErr
		}

		if expectedCount == 0 {
			return true, nil
		}

		if len(crtbs.Items) == expectedCount {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		if crtbs != nil {
			return crtbs, fmt.Errorf("timed out waiting for ClusterRoleTemplateBindings count to match expected: %d, actual: %d, error: %w",
				expectedCount, len(crtbs.Items), err)
		}
		return nil, err
	}

	return crtbs, nil
}

// GetRoleBindingsForCRTBs gets RoleBindings based on ClusterRoleTemplateBindings
func GetRoleBindingsForCRTBs(client *rancher.Client, crtbs *v3.ClusterRoleTemplateBindingList) (*rbacv1.RoleBindingList, error) {
	var downstreamRBs rbacv1.RoleBindingList

	for _, crtb := range crtbs.Items {
		roleTemplateName := crtb.RoleTemplateName
		if strings.Contains(roleTemplateName, "rt") {
			listOpt := metav1.ListOptions{
				FieldSelector: "metadata.name=" + roleTemplateName,
			}
			roleTemplateList, err := ListRoleTemplates(client, listOpt)
			if err != nil {
				return nil, err
			}
			if len(roleTemplateList.Items) > 0 {
				roleTemplateName = roleTemplateList.Items[0].RoleTemplateNames[0]
			}
		}

		nameSelector := fmt.Sprintf("metadata.name=%s-%s", crtb.Name, roleTemplateName)
		namespaceSelector := fmt.Sprintf("metadata.namespace=%s", crtb.ClusterName)
		combinedSelector := fmt.Sprintf("%s,%s", nameSelector, namespaceSelector)
		downstreamRBsForCRTB, err := ListRoleBindings(client, clusterapi.LocalCluster, "", metav1.ListOptions{
			FieldSelector: combinedSelector,
		})
		if err != nil {
			return nil, err
		}

		downstreamRBs.Items = append(downstreamRBs.Items, downstreamRBsForCRTB.Items...)
	}

	return &downstreamRBs, nil
}

// GetClusterRoleBindingsForCRTBs gets ClusterRoleBindings based on ClusterRoleTemplateBindings using labels
func GetClusterRoleBindingsForCRTBs(client *rancher.Client, crtbs *v3.ClusterRoleTemplateBindingList) (*rbacv1.ClusterRoleBindingList, error) {
	var downstreamCRBs rbacv1.ClusterRoleBindingList

	for _, crtb := range crtbs.Items {
		labelKey := fmt.Sprintf("%s_%s", crtb.ClusterName, crtb.Name)
		req, err := labels.NewRequirement(labelKey, selection.In, []string{MembershipBindingOwnerLabel})
		if err != nil {
			return nil, err
		}

		selector := labels.NewSelector().Add(*req)
		downstreamCRBsForCRTB, err := ListClusterRoleBindings(client, clusterapi.LocalCluster, metav1.ListOptions{
			LabelSelector: selector.String(),
		})
		if err != nil {
			return nil, err
		}

		downstreamCRBs.Items = append(downstreamCRBs.Items, downstreamCRBsForCRTB.Items...)
	}

	return &downstreamCRBs, nil
}

// GetClusterRoleBindingsForUsers gets ClusterRoleBindings where users from CRTBs are subjects
func GetClusterRoleBindingsForUsers(client *rancher.Client, crtbs *v3.ClusterRoleTemplateBindingList) ([]rbacv1.ClusterRoleBinding, error) {
	var userCRBs []rbacv1.ClusterRoleBinding

	for _, crtb := range crtbs.Items {
		crbs, err := ListClusterRoleBindings(client, clusterapi.LocalCluster, metav1.ListOptions{})
		if err != nil {
			return nil, err
		}

		for _, crb := range crbs.Items {
			for _, subject := range crb.Subjects {
				if subject.Kind == "User" && subject.Name == crtb.UserName {
					userCRBs = append(userCRBs, crb)
				}
			}
		}
	}

	return userCRBs, nil
}

// SetAggregatedClusterRoleFeatureFlag sets the aggregated cluster role feature flag to the specified value
func SetAggregatedClusterRoleFeatureFlag(client *rancher.Client, value bool) error {
	feature, err := client.WranglerContext.Mgmt.Feature().Get(AggregatedRoleTemplatesFeatureFlag, metav1.GetOptions{})
	if err != nil {
		return fmt.Errorf("failed to fetch feature %s: %w", AggregatedRoleTemplatesFeatureFlag, err)
	}

	feature.Spec.Value = &value

	_, err = client.WranglerContext.Mgmt.Feature().Update(feature)
	if err != nil {
		return fmt.Errorf("failed to update feature %s: %w", AggregatedRoleTemplatesFeatureFlag, err)
	}

	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		updatedFeature, getErr := client.WranglerContext.Mgmt.Feature().Get(AggregatedRoleTemplatesFeatureFlag, metav1.GetOptions{})
		if getErr != nil {
			return false, nil
		}

		if updatedFeature.Spec.Value != nil && *updatedFeature.Spec.Value == value {
			return true, nil
		}

		return false, nil
	})
}

// IsFeatureEnabled checks if a feature is enabled based on its Spec.Value
func IsFeatureEnabled(client *rancher.Client, featureName string) (bool, error) {
	feature, err := client.WranglerContext.Mgmt.Feature().Get(AggregatedRoleTemplatesFeatureFlag, metav1.GetOptions{})
	if err != nil {
		return false, fmt.Errorf("failed to fetch feature %s: %w", featureName, err)
	}

	return feature.Spec.Value != nil && *feature.Spec.Value, nil
}

// WaitForClusterRoleExistence polls until the ClusterRole exists or does not exist, based on shouldExist.
func WaitForClusterRoleExistence(client *rancher.Client, clusterID, clusterRoleName string, shouldExist bool) error {
	wranglerCtx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := wranglerCtx.RBAC.ClusterRole().Get(clusterRoleName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return !shouldExist, nil
		}
		if err != nil {
			return false, err
		}
		return shouldExist, nil
	})
}

// WaitForRoleExistence polls until the Role exists or does not exist, based on shouldExist.
func WaitForRoleExistence(client *rancher.Client, clusterID, namespaceName, roleName string, shouldExist bool) error {
	wranglerCtx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return err
	}

	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := wranglerCtx.RBAC.Role().Get(namespaceName, roleName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return !shouldExist, nil
		}
		if err != nil {
			return false, err
		}
		return shouldExist, nil
	})
}

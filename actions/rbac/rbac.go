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
	extauthz "github.com/rancher/shepherd/extensions/kubeapi/authorization"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wrangler"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/sirupsen/logrus"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/selection"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type Role string

const (
	Admin                       Role = "admin"
	BaseUser                    Role = "user-base"
	StandardUser                Role = "user"
	ClusterOwner                Role = "cluster-owner"
	ClusterMember               Role = "cluster-member"
	ProjectOwner                Role = "project-owner"
	ProjectMember               Role = "project-member"
	CreateNS                    Role = "create-ns"
	ReadOnly                    Role = "read-only"
	CustomManageProjectMember   Role = "projectroletemplatebindings-manage"
	CrtbView                    Role = "clusterroletemplatebindings-view"
	PrtbView                    Role = "projectroletemplatebindings-view"
	ProjectsCreate              Role = "projects-create"
	ProjectsView                Role = "projects-view"
	ManageWorkloads             Role = "workloads-manage"
	ActiveStatus                     = "active"
	ForbiddenError                   = "403 Forbidden"
	RancherDeploymentNamespace       = "cattle-system"
	DefaultNamespace                 = "fleet-default"
	RancherDeploymentName            = "rancher"
	CattleResyncEnvVarName           = "CATTLE_RESYNC_DEFAULT"
	LocalCluster                     = "local"
	UserKind                         = "User"
	ImageName                        = "nginx"
	ManageUsersVerb                  = "manage-users"
	UpdatePsaVerb                    = "updatepsa"
	ManagementAPIGroup               = "management.cattle.io"
	UsersResource                    = "users"
	UserAttributeResource            = "userattribute"
	GroupsResource                   = "groups"
	GroupMembersResource             = "groupmembers"
	ProjectResource                  = "projects"
	PrtbResource                     = "projectroletemplatebindings"
	SecretsResource                  = "secrets"
	ClusterContext                   = "cluster"
	ProjectContext                   = "project"
	GrbOwnerLabel                    = "authz.management.cattle.io/grb-owner"
	GlobalDataNS                     = "cattle-global-data"
	MembershipBindingOwnerLabel      = "membership-binding-owner"
	PSALabelKey                      = "pod-security.kubernetes.io/"
	PSAEnforceLabelKey               = "pod-security.kubernetes.io/enforce"
	PSAWarnLabelKey                  = "pod-security.kubernetes.io/warn"
	PSAAuditLabelKey                 = "pod-security.kubernetes.io/audit"
	PSAPrivilegedPolicy              = "privileged"
	PSABaselinePolicy                = "baseline"
	PSARestrictedPolicy              = "restricted"
	PSAEnforceVersionLabelKey        = "pod-security.kubernetes.io/enforce-version"
	PSAWarnVersionLabelKey           = "pod-security.kubernetes.io/warn-version"
	PSAAuditVersionLabelKey          = "pod-security.kubernetes.io/audit-version"
	PSALatestValue                   = "latest"
)

func (r Role) String() string {
	return string(r)
}

// AddUserWithRoleToCluster creates a user based on the global role and then adds the user to cluster with provided permissions.
func AddUserWithRoleToCluster(client *rancher.Client, globalRole, role string, cluster *management.Cluster, project *v3.Project) (*management.User, *rancher.Client, error) {
	standardUser, standardUserClient, err := SetupUser(client, globalRole)
	if err != nil {
		return nil, nil, err
	}

	roleContext, err := GetRoleTemplateContext(client, role)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get context for role %s: %w", role, err)
	}

	switch roleContext {
	case ProjectContext:
		if project == nil {
			return nil, nil, fmt.Errorf("project is required for project-scoped role: %s", role)
		}
		_, err = CreateProjectRoleTemplateBinding(client, standardUser, project, role)
		if err != nil {
			return nil, nil, err
		}
	case ClusterContext:
		if cluster == nil {
			return nil, nil, fmt.Errorf("cluster is required for cluster-scoped role: %s", role)
		}
		_, err = CreateClusterRoleTemplateBinding(client, cluster.ID, standardUser, role)
		if err != nil {
			return nil, nil, err
		}
	default:
		return nil, nil, fmt.Errorf("unknown context %s for role %s", roleContext, role)
	}

	standardUserClient, err = standardUserClient.ReLogin()
	if err != nil {
		return nil, nil, err
	}

	return standardUser, standardUserClient, nil
}

// SetupUser is a helper to create a user with the specified global role and a client for the user.
func SetupUser(client *rancher.Client, globalRoles ...string) (user *management.User, userClient *rancher.Client, err error) {
	user, err = users.CreateUserWithRole(client, users.UserConfig(), globalRoles...)
	if err != nil {
		return
	}
	userClient, err = client.AsUser(user)
	if err != nil {
		return
	}
	return
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

// GetRoleBindings is a helper function to fetch rolebindings for a user
func GetRoleBindings(rancherClient *rancher.Client, clusterID string, userID string) ([]rbacv1.RoleBinding, error) {
	logrus.Infof("Getting role bindings for user %s in cluster %s", userID, clusterID)
	listOpt := metav1.ListOptions{}
	roleBindings, err := rbacapi.ListRoleBindings(rancherClient, clusterID, "", listOpt)
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
	logrus.Infof("Getting all bindings for user %s", userID)
	bindings := make(map[string]interface{})

	roleBindings, err := GetRoleBindings(rancherClient, rbacapi.LocalCluster, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get role bindings: %w", err)
	}
	bindings["RoleBindings"] = roleBindings

	logrus.Info("Getting cluster role bindings")
	clusterRoleBindings, err := rbacapi.ListClusterRoleBindings(rancherClient, rbacapi.LocalCluster, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role bindings: %w", err)
	}
	bindings["ClusterRoleBindings"] = clusterRoleBindings.Items

	logrus.Info("Getting global role bindings")
	globalRoleBindings, err := rancherClient.Management.GlobalRoleBinding.ListAll(&types.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to list global role bindings: %w", err)
	}
	bindings["GlobalRoleBindings"] = globalRoleBindings.Data

	logrus.Info("Getting cluster role template bindings")
	clusterRoleTemplateBindings, err := rancherClient.Management.ClusterRoleTemplateBinding.List(&types.ListOpts{})
	if err != nil {
		return nil, fmt.Errorf("failed to list cluster role template bindings: %w", err)
	}
	bindings["ClusterRoleTemplateBindings"] = clusterRoleTemplateBindings.Data

	logrus.Info("All bindings retrieved successfully")
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

// GetGlobalRoleByName is a helper function to fetch global role by name
func GetGlobalRoleByName(client *rancher.Client, globalRoleName string) (*v3.GlobalRole, error) {
	var matchingGlobalRole *v3.GlobalRole

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		grlist, err := client.WranglerContext.Mgmt.GlobalRole().List(metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		for _, gr := range grlist.Items {
			if gr.Name == globalRoleName {
				matchingGlobalRole = &gr
				return true, nil
			}
		}

		return false, nil
	})

	if err != nil {
		return nil, fmt.Errorf("error while polling for global role: %w", err)
	}

	return matchingGlobalRole, nil
}

// GetGlobalRoleBindingByName is a helper function to fetch global role binding by name
func GetGlobalRoleBindingByName(client *rancher.Client, globalRoleBindingName string) (*v3.GlobalRoleBinding, error) {
	var matchingGlobalRoleBinding *v3.GlobalRoleBinding

	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		grblist, err := client.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
		if err != nil {
			return false, err
		}

		for _, grb := range grblist.Items {
			if grb.Name == globalRoleBindingName {
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

// GetClusterRoleRules is a helper function to fetch rules for a cluster role
func GetClusterRoleRules(client *rancher.Client, clusterID string, clusterRoleName string) ([]rbacv1.PolicyRule, error) {
	var ctx *wrangler.Context
	var err error

	if clusterID != rbacapi.LocalCluster {
		ctx, err = client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
		if err != nil {
			return nil, fmt.Errorf("failed to get downstream context: %w", err)
		}
	} else {
		ctx = client.WranglerContext
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

// CreateRoleTemplate creates a cluster or project role template with the provided rules using wrangler context
func CreateRoleTemplate(client *rancher.Client, context string, rules []rbacv1.PolicyRule, inheritedRoles []*v3.RoleTemplate, external bool, externalRules []rbacv1.PolicyRule) (*v3.RoleTemplate, error) {
	var roleTemplateNames []string
	for _, inheritedRole := range inheritedRoles {
		if inheritedRole != nil {
			roleTemplateNames = append(roleTemplateNames, inheritedRole.Name)
		}
	}

	displayName := namegen.AppendRandomString("role-template")

	roleTemplate := &v3.RoleTemplate{
		ObjectMeta: metav1.ObjectMeta{
			Name: displayName,
		},
		Context:           context,
		Rules:             rules,
		DisplayName:       displayName,
		RoleTemplateNames: roleTemplateNames,
		External:          external,
		ExternalRules:     externalRules,
	}

	createdRoleTemplate, err := client.WranglerContext.Mgmt.RoleTemplate().Create(roleTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to create RoleTemplate: %w", err)
	}

	return GetRoleTemplateByName(client, createdRoleTemplate.Name)
}

// CreateClusterRoleTemplateBinding creates a cluster role template binding for the user with the provided role template using wrangler context
func CreateClusterRoleTemplateBinding(client *rancher.Client, clusterID string, user *management.User, roleTemplateID string) (*v3.ClusterRoleTemplateBinding, error) {
	crtbObj := &v3.ClusterRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    clusterID,
			GenerateName: "crtb-",
		},
		ClusterName:      clusterID,
		UserName:         user.ID,
		RoleTemplateName: roleTemplateID,
	}

	crtb, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Create(crtbObj)
	if err != nil {
		return nil, err
	}

	err = WaitForCrtbStatus(client, crtb.Namespace, crtb.Name)
	if err != nil {
		return nil, err
	}

	userClient, err := client.AsUser(user)
	if err != nil {
		return nil, fmt.Errorf("client as user %s: %w", user.Name, err)
	}

	err = extauthz.WaitForAllowed(userClient, clusterID, nil)
	if err != nil {
		return nil, err
	}

	return crtb, nil
}

// GetClusterRoleTemplateBindingsForUser fetches clusterroletemplatebindings for a specific user
func GetClusterRoleTemplateBindingsForUser(rancherClient *rancher.Client, userID string) (*v3.ClusterRoleTemplateBinding, error) {
	var matchingCRTB *v3.ClusterRoleTemplateBinding
	err := kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, pollErr error) {
		crtbList, err := rbacapi.ListClusterRoleTemplateBindings(rancherClient, metav1.ListOptions{})
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

// WaitForCrtbStatus waits for the CRTB to reach the Completed status or checks for its existence if status field is not supported (older Rancher versions)
func WaitForCrtbStatus(client *rancher.Client, crtbNamespace, crtbName string) error {
	ctx, cancel := context.WithTimeout(context.Background(), defaults.OneMinuteTimeout)
	defer cancel()

	err := kwait.PollUntilContextTimeout(ctx, defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (done bool, err error) {
		crtb, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Get(crtbNamespace, crtbName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}

		if crtb.Status.Summary == "Completed" {
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

// CreateProjectRoleTemplateBinding creates a project role template binding for the user with the provided role template using wrangler context
func CreateProjectRoleTemplateBinding(client *rancher.Client, user *management.User, project *v3.Project, roleTemplateID string) (*v3.ProjectRoleTemplateBinding, error) {
	projectName := fmt.Sprintf("%s:%s", project.Namespace, project.Name)

	prtbNamespace := project.Name
	if project.Status.BackingNamespace != "" {
		prtbNamespace = fmt.Sprintf("%s-%s", project.Spec.ClusterName, project.Name)
	}

	prtbObj := &v3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    prtbNamespace,
			GenerateName: "prtb-",
		},
		ProjectName:      projectName,
		UserName:         user.ID,
		RoleTemplateName: roleTemplateID,
	}

	prtbObj, err := client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(prtbObj)
	if err != nil {
		return nil, err
	}

	prtb, err := WaitForPrtbExistence(client, project, prtbObj, user)

	if err != nil {
		return nil, err
	}

	userClient, err := client.AsUser(user)
	if err != nil {
		return nil, fmt.Errorf("client as user %s: %w", user.Name, err)
	}

	err = extauthz.WaitForAllowed(userClient, project.Namespace, nil)
	if err != nil {
		return nil, err
	}

	return prtb, nil
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
		crtbs, pollErr = rbacapi.ListClusterRoleTemplateBindings(client, metav1.ListOptions{
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

		logrus.Infof("Waiting for ClusterRoleTemplateBindings count to match, current: %d, expected: %d",
			len(crtbs.Items), expectedCount)
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
			roleTemplateList, err := rbacapi.ListRoleTemplates(client, listOpt)
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
		downstreamRBsForCRTB, err := rbacapi.ListRoleBindings(client, rbacapi.LocalCluster, "", metav1.ListOptions{
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
		downstreamCRBsForCRTB, err := rbacapi.ListClusterRoleBindings(client, rbacapi.LocalCluster, metav1.ListOptions{
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
		crbs, err := rbacapi.ListClusterRoleBindings(client, rbacapi.LocalCluster, metav1.ListOptions{})
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

// GetRoleBindingsForUsers gets RoleBindings where users are subjects in specific namespaces
func GetRoleBindingsForUsers(client *rancher.Client, userName string, namespaces []string) ([]rbacv1.RoleBinding, error) {
	var userRBs []rbacv1.RoleBinding

	for _, namespace := range namespaces {
		rbs, err := rbacapi.ListRoleBindings(client, rbacapi.LocalCluster, namespace, metav1.ListOptions{})
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

// CreateGlobalRoleWithInheritedClusterRolesWrangler creates a global role with inherited cluster roles
func CreateGlobalRoleWithInheritedClusterRolesWrangler(client *rancher.Client, inheritedRoles []string) (*v3.GlobalRole, error) {
	globalRole := v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("testgr"),
		},
		InheritedClusterRoles: inheritedRoles,
	}

	createdGlobalRole, err := client.WranglerContext.Mgmt.GlobalRole().Create(&globalRole)
	if err != nil {
		return nil, fmt.Errorf("failed to create global role with inherited cluster roles: %w", err)
	}

	return createdGlobalRole, nil
}

// CreateGroupClusterRoleTemplateBinding creates Cluster Role Template bindings
func CreateGroupClusterRoleTemplateBinding(client *rancher.Client, clusterID string, groupPrincipalID string, roleTemplateID string) (*v3.ClusterRoleTemplateBinding, error) {
	crtbObj := &v3.ClusterRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    clusterID,
			GenerateName: "crtb-",
			Annotations: map[string]string{
				"field.cattle.io/creatorId": client.UserID,
			},
		},
		ClusterName:        clusterID,
		GroupPrincipalName: groupPrincipalID,
		RoleTemplateName:   roleTemplateID,
	}

	crtb, err := client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Create(crtbObj)
	if err != nil {
		return nil, err
	}

	err = WaitForCrtbStatus(client, crtb.Namespace, crtb.Name)
	if err != nil {
		return nil, err
	}

	return crtb, nil
}

// CreateGroupProjectRoleTemplateBinding creates Project Role Template bindings for groups
func CreateGroupProjectRoleTemplateBinding(client *rancher.Client, projectID string, projectNamespace string, groupPrincipalID string, roleTemplateID string) (*v3.ProjectRoleTemplateBinding, error) {
	prtbObj := &v3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    projectNamespace,
			GenerateName: "prtb-",
		},
		ProjectName:        projectID,
		GroupPrincipalName: groupPrincipalID,
		RoleTemplateName:   roleTemplateID,
	}

	prtb, err := client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(prtbObj)
	if err != nil {
		return nil, err
	}

	return prtb, nil
}

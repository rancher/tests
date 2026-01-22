package rbac

import (
	"context"
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	extauthz "github.com/rancher/shepherd/extensions/kubeapi/authorization"
	"github.com/rancher/shepherd/extensions/unstructured"
	"github.com/rancher/shepherd/pkg/api/scheme"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

// CreateRole is a helper function that uses the dynamic client to create a role on a namespace for a specific cluster.
func CreateRole(client *rancher.Client, clusterName string, role *rbacv1.Role) (*rbacv1.Role, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterName)
	if err != nil {
		return nil, err
	}

	roleResource := dynamicClient.Resource(RoleGroupVersionResource).Namespace(role.Namespace)

	unstructuredResp, err := roleResource.Create(context.Background(), unstructured.MustToUnstructured(role), metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	newRole := &rbacv1.Role{}
	err = scheme.Scheme.Convert(unstructuredResp, newRole, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	return newRole, nil
}

// CreateRoleBinding is a helper function that uses the dynamic client to create a rolebinding on a namespace for a specific cluster.
func CreateRoleBinding(client *rancher.Client, clusterName, roleBindingName, namespace, roleName string, subject rbacv1.Subject) (*rbacv1.RoleBinding, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterName)
	if err != nil {
		return nil, err
	}

	roleBinding := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      roleBindingName,
			Namespace: namespace,
		},
		Subjects: []rbacv1.Subject{subject},
		RoleRef: rbacv1.RoleRef{
			APIGroup: rbacv1.SchemeGroupVersion.Group,
			Kind:     "Role",
			Name:     roleName,
		},
	}

	roleBindingResource := dynamicClient.Resource(RoleBindingGroupVersionResource).Namespace(namespace)

	unstructuredResp, err := roleBindingResource.Create(context.Background(), unstructured.MustToUnstructured(roleBinding), metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	newRoleBinding := &rbacv1.RoleBinding{}
	err = scheme.Scheme.Convert(unstructuredResp, newRoleBinding, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	return newRoleBinding, nil
}

// CreateGlobalRole is a helper function that uses the dynamic client to create a global role in the local cluster.
func CreateGlobalRole(client *rancher.Client, globalRole *v3.GlobalRole) (*v3.GlobalRole, error) {
	dynamicClient, err := client.GetDownStreamClusterClient(clusterapi.LocalCluster)
	if err != nil {
		return nil, err
	}

	globalRoleResource := dynamicClient.Resource(GlobalRoleGroupVersionResource)
	unstructuredResp, err := globalRoleResource.Create(context.TODO(), unstructured.MustToUnstructured(globalRole), metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	newGlobalRole := &v3.GlobalRole{}
	err = scheme.Scheme.Convert(unstructuredResp, newGlobalRole, unstructuredResp.GroupVersionKind())
	if err != nil {
		return nil, err
	}

	return newGlobalRole, nil
}

// CreateGlobalRoleBinding creates a global role binding for the user with the provided global role using wrangler context
func CreateGlobalRoleBinding(client *rancher.Client, globalRoleName, userName, groupPrincipalName, userPrincipalName string) (*v3.GlobalRoleBinding, error) {
	grbObj := &v3.GlobalRoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "grb-",
		},
		UserName:           userName,
		GroupPrincipalName: groupPrincipalName,
		UserPrincipalName:  userPrincipalName,
		GlobalRoleName:     globalRoleName,
	}

	grb, err := client.WranglerContext.Mgmt.GlobalRoleBinding().Create(grbObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create global role binding for global role %s: %w", globalRoleName, err)
	}

	err = WaitForGrbExistence(client, grb.Name)
	if err != nil {
		return nil, err
	}

	return grb, nil
}

// WaitForGrbExistence waits until the GlobalRoleBinding exists based on the provided field (User, UserPrincipal, or Group)
func WaitForGrbExistence(client *rancher.Client, grbName string) error {
	return kwait.PollUntilContextTimeout(context.TODO(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		_, err := client.WranglerContext.Mgmt.GlobalRoleBinding().Get(grbName, metav1.GetOptions{})
		if err != nil {
			return false, nil
		}
		return true, nil
	})
}

// CreateRoleTemplate creates a cluster or project role template with the provided rules using wrangler context
func CreateRoleTemplate(client *rancher.Client, context string, rules []rbacv1.PolicyRule, inheritedRoleTemplates []*v3.RoleTemplate, external, locked bool, externalRules []rbacv1.PolicyRule) (*v3.RoleTemplate, error) {
	var roleTemplateNames []string
	for _, inheritedRole := range inheritedRoleTemplates {
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
		Locked:            locked,
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

// CreateProjectRoleTemplateBinding creates a project role template binding for the user with the provided role template using wrangler context
func CreateProjectRoleTemplateBinding(client *rancher.Client, user *management.User, project *v3.Project, roleTemplateID string) (*v3.ProjectRoleTemplateBinding, error) {
	projectName := fmt.Sprintf("%s:%s", project.Namespace, project.Name)

	prtbNamespace := project.Name
	if project.Status.BackingNamespace != "" {
		prtbNamespace = fmt.Sprintf("%s-%s", project.Spec.ClusterName, project.Name)
	}

	prtbObj := &v3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegen.AppendRandomString("prtb-"),
			Namespace: prtbNamespace,
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

// CreateGroupClusterRoleTemplateBinding creates Cluster Role Template bindings for groups with the provided role template using wrangler context
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

// CreateGroupProjectRoleTemplateBinding creates Project Role Template bindings for groups with the provided role template using wrangler context
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

// CreateGlobalRoleWithInheritedClusterRolesWrangler creates a global role with inherited cluster roles using wrangler context
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

package rbac

import (
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateRoleBinding is a helper function that uses the wrangler context to create a rolebinding on a namespace for a specific cluster.
func CreateRoleBinding(client *rancher.Client, clusterID, namespace, roleName string, subject rbacv1.Subject) (*rbacv1.RoleBinding, error) {
	roleBindingName := namegen.AppendRandomString("rolebinding-")

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

	newRoleBinding, err := extrbacapi.CreateRoleBinding(client, clusterID, roleBinding)
	if err != nil {
		return nil, err
	}

	return newRoleBinding, nil
}

// CreateGlobalRoleBinding is a helper function that uses the wrangler context to create a global role binding for the user with the provided global role
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

	grb, err := extrbacapi.CreateGlobalRoleBinding(client, grbObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create global role binding for global role %s: %w", globalRoleName, err)
	}

	return grb, nil
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

	createdRoleTemplate, err := extrbacapi.CreateRoleTemplate(client, roleTemplate)
	if err != nil {
		return nil, fmt.Errorf("failed to create RoleTemplate: %w", err)
	}

	return createdRoleTemplate, nil
}

// CreateClusterRoleTemplateBinding creates a cluster role template binding for the user with the provided role template using wrangler context
func CreateClusterRoleTemplateBinding(client *rancher.Client, clusterID string, userName string, roleTemplateID string) (*v3.ClusterRoleTemplateBinding, error) {
	crtbObj := &v3.ClusterRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Namespace:    clusterID,
			GenerateName: "crtb-",
		},
		ClusterName:      clusterID,
		UserName:         userName,
		RoleTemplateName: roleTemplateID,
	}

	createdCrtb, err := extrbacapi.CreateClusterRoleTemplateBinding(client, crtbObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClusterRoleTemplateBinding for cluster %s: %w", clusterID, err)
	}

	crtb, err := extrbacapi.WaitForClusterRoleTemplateBindingToExist(client, createdCrtb.Namespace, createdCrtb.Name)
	if err != nil {
		return nil, fmt.Errorf("error waiting for ClusterRoleTemplateBinding %s to exist: %w", createdCrtb.Name, err)
	}

	return crtb, nil
}

// CreateProjectRoleTemplateBinding creates a project role template binding for the user with the provided role template using wrangler context
func CreateProjectRoleTemplateBinding(client *rancher.Client, userName string, project *v3.Project, roleTemplateID string) (*v3.ProjectRoleTemplateBinding, error) {
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
		UserName:         userName,
		RoleTemplateName: roleTemplateID,
	}

	createdPrtb, err := extrbacapi.CreateProjectRoleTemplateBinding(client, prtbObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create ProjectRoleTemplateBinding for project %s: %w", projectName, err)
	}

	prtb, err := extrbacapi.WaitForProjectRoleTemplateBindingToExist(client, projectName, createdPrtb.Namespace, createdPrtb.Name, userName)
	if err != nil {
		return nil, fmt.Errorf("error waiting for ProjectRoleTemplateBinding %s to exist: %w", createdPrtb.Name, err)
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

	crtb, err := extrbacapi.CreateClusterRoleTemplateBinding(client, crtbObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create ClusterRoleTemplateBinding for cluster %s: %w", clusterID, err)
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

	prtb, err := extrbacapi.CreateProjectRoleTemplateBinding(client, prtbObj)
	if err != nil {
		return nil, fmt.Errorf("failed to create ProjectRoleTemplateBinding for project %s: %w", projectID, err)
	}

	return prtb, nil
}

// CreateGlobalRoleWithInheritedClusterRoles creates a global role with inherited cluster roles using wrangler context
func CreateGlobalRoleWithInheritedClusterRoles(client *rancher.Client, inheritedRoles []string) (*v3.GlobalRole, error) {
	globalRole := v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("testgr"),
		},
		InheritedClusterRoles: inheritedRoles,
	}

	createdGlobalRole, err := extrbacapi.CreateGlobalRole(client, &globalRole)
	if err != nil {
		return nil, fmt.Errorf("failed to create global role with inherited cluster roles: %w", err)
	}

	return createdGlobalRole, nil
}

// CreateGlobalRoleWithInheritedNamespacedRules creates a global role with inherited namespaced rules using wrangler context
func CreateGlobalRoleWithInheritedNamespacedRules(client *rancher.Client, inheritedClusterRole []string, inheritedNamespacedRules map[string][]rbacv1.PolicyRule) (*v3.GlobalRole, error) {
	globalRole := &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("test-inr"),
		},
		InheritedClusterRoles:    inheritedClusterRole,
		InheritedNamespacedRules: inheritedNamespacedRules,
	}

	createdGlobalRole, err := extrbacapi.CreateGlobalRole(client, globalRole)
	if err != nil {
		return nil, fmt.Errorf("failed to create global role with inherited namespaced rules: %w", err)
	}

	return createdGlobalRole, nil
}

// CreateGlobalRoleWithAllRules creates a global role with rules, inherited cluster roles, and inherited namespaced rules using wrangler context
func CreateGlobalRoleWithAllRules(client *rancher.Client, inheritedClusterRole []string, rules []rbacv1.PolicyRule, namespacedRules map[string][]rbacv1.PolicyRule, inheritedNamespacedRules map[string][]rbacv1.PolicyRule) (*v3.GlobalRole, error) {
	globalRole := &v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: namegen.AppendRandomString("test-gr"),
		},
		Rules:                    rules,
		InheritedClusterRoles:    inheritedClusterRole,
		NamespacedRules:          namespacedRules,
		InheritedNamespacedRules: inheritedNamespacedRules,
	}

	createdGlobalRole, err := extrbacapi.CreateGlobalRole(client, globalRole)
	if err != nil {
		return nil, fmt.Errorf("failed to create global role: %w", err)
	}

	return createdGlobalRole, nil
}

// GrantUserCRDUpdatePermissions creates a ClusterRole and ClusterRoleBinding
// to grant a specific user Kubernetes API permissions to get, patch, and update User CRDs.
// This allows the user's requests to bypass the Kubernetes API server's RBAC checks
// so they can be evaluated directly by the admission webhook. Returns a cleanup function.
func GrantUserCRDUpdatePermissions(client *rancher.Client, clusterID, username string) (error) {
    testerRoleName := namegen.AppendRandomString("webhook-tester-role-")
    testerRole := &rbacv1.ClusterRole{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "rbac.authorization.k8s.io/v1",
            Kind:       "ClusterRole",
        },
        ObjectMeta: metav1.ObjectMeta{
            Name: testerRoleName,
        },
        Rules: []rbacv1.PolicyRule{
            {
                APIGroups: []string{"management.cattle.io"},
                Resources: []string{"users"},
                Verbs:     []string{"get", "patch", "update"},
            },
        },
    }
    _, err := extrbacapi.CreateClusterRole(client, clusterID, testerRole)
    if err != nil {
        return fmt.Errorf("failed to create webhook-tester-role: %w", err)
    }

    testerBindingName := namegen.AppendRandomString("binding-")
    testerBinding := &rbacv1.ClusterRoleBinding{
        TypeMeta: metav1.TypeMeta{
            APIVersion: "rbac.authorization.k8s.io/v1",
            Kind:       "ClusterRoleBinding",
        },
        ObjectMeta: metav1.ObjectMeta{
            Name: testerBindingName,
        },
        Subjects: []rbacv1.Subject{
            {
                Kind:     "User",
                Name:     username,
                APIGroup: "rbac.authorization.k8s.io",
            },
        },
        RoleRef: rbacv1.RoleRef{
            Kind:     "ClusterRole",
            Name:     testerRoleName,
            APIGroup: "rbac.authorization.k8s.io",
        },
    }
    _, err = extrbacapi.CreateClusterRoleBinding(client, clusterID, testerBinding)
    if err != nil {
        return fmt.Errorf("failed to create webhook-tester-binding: %w", err)
    }

    return nil
}
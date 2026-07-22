package globalroles

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func newCustomGlobalRole(verbs []string) v3.GlobalRole {
	name := namegen.AppendRandomString("custom-global-role-")
	return v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{rbacapi.ManagementAPIGroup},
				Resources: []string{rbacapi.UsersResource},
				Verbs:     verbs,
			},
		},
	}
}

func customGlobalRoleDelete() *v3.GlobalRole {
	globalRole := newCustomGlobalRole([]string{"delete", "get", "list"})
	return &globalRole
}

func customGlobalRoleEdit() *v3.GlobalRole {
	globalRole := newCustomGlobalRole([]string{"patch", "update", "get", "list"})
	return &globalRole
}

func customGlobalRoleManageUsers() *v3.GlobalRole {
	globalRole := newCustomGlobalRole([]string{rbacapi.ManageUsersVerb, "patch", "update", "delete", "get", "list"})
	return &globalRole
}

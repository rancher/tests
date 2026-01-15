package globalroles

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// newRestrictedAdminReplacementTemplate is a constructor that creates the restricted-admin-replacement global role
func newRestrictedAdminReplacementTemplate(globalRoleName string) v3.GlobalRole {
	return v3.GlobalRole{
		ObjectMeta: metav1.ObjectMeta{
			Name: globalRoleName,
		},
		Rules: []rbacv1.PolicyRule{
			{
				APIGroups: []string{""},
				Resources: []string{"secrets"},
				Verbs:     []string{"create"},
			},
			{
				APIGroups: []string{"catalog.cattle.io"},
				Resources: []string{"clusterrepos"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"clustertemplates"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"clustertemplaterevisions"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"globalrolebindings"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"globalroles"},
				Verbs: []string{
					"delete", "deletecollection", "get", "list",
					"patch", "create", "update", "watch",
				},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"users", "userattribute", "groups", "groupmembers"},
				Verbs: []string{
					"delete", "get", "list",
					"patch", "create", "update", "watch",
				},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"podsecurityadmissionconfigurationtemplates"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"authconfigs"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"nodedrivers"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"kontainerdrivers"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"roletemplates"},
				Verbs:     []string{"*"},
			},
			{
				APIGroups: []string{"management.cattle.io"},
				Resources: []string{"templates", "templateversions"},
				Verbs:     []string{"*"},
			},
		},
		InheritedClusterRoles: []string{
			"cluster-owner",
		},
		InheritedFleetWorkspacePermissions: &v3.FleetWorkspacePermission{
			ResourceRules: []rbacv1.PolicyRule{
				{
					APIGroups: []string{"fleet.cattle.io"},
					Resources: []string{
						"clusterregistrationtokens", "gitreporestrictions", "clusterregistrations",
						"clusters", "gitrepos", "bundles", "bundledeployments", "clustergroups",
					},
					Verbs: []string{"*"},
				},
			},
			WorkspaceVerbs: []string{"get", "list", "update", "create", "delete"},
		},
	}
}

var (
	manageUsersVerb = rbacv1.PolicyRule{
		Verbs:     []string{rbacapi.ManageUsersVerb},
		APIGroups: []string{rbacapi.ManagementAPIGroup},
		Resources: []string{rbacapi.UsersResource, rbacapi.UserAttributeResource, rbacapi.GroupsResource, rbacapi.GroupMembersResource},
	}
)

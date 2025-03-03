package clusterandprojectroles

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	rbac "github.com/rancher/tests/actions/rbac"
	rbacv1 "k8s.io/api/rbac/v1"
	v1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	restrictedAdminReplacementRole = v3.GlobalRole{
		ObjectMeta: v1.ObjectMeta{
			Name: "",
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
				Verbs:     []string{"*"},
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
)

func createRestrictedAdminReplacementGlobalRole(client *rancher.Client) (*v3.GlobalRole, error) {
	restrictedAdminReplacementRole.Name = namegen.AppendRandomString("restricted-admin-replacement")
	createdGlobalRole, err := client.WranglerContext.Mgmt.GlobalRole().Create(&restrictedAdminReplacementRole)
	if err != nil {
		return nil, err
	}

	createdGlobalRole, err = rbac.GetGlobalRoleByName(client, createdGlobalRole.Name)
	if err != nil {
		return nil, err
	}

	return createdGlobalRole, err
}

func createRestrictedAdminReplacementGlobalRoleAndUser(client *rancher.Client) (*v3.GlobalRole, *management.User, error) {
	createdGlobalRole, err := createRestrictedAdminReplacementGlobalRole(client)
	if err != nil {
		return nil, nil, err
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String(), createdGlobalRole.Name)
	if err != nil {
		return nil, nil, err
	}

	return createdGlobalRole, createdUser, err
}

func getGlobalSettings(client *rancher.Client, clusterID string) ([]string, error) {
	context, err := client.WranglerContext.DownStreamClusterWranglerContext(clusterID)
	if err != nil {
		return nil, err
	}

	settings, err := context.Mgmt.Setting().List(v1.ListOptions{})
	if err != nil {
		return nil, err
	}

	globalSettings := []string{}
	for _, gs := range settings.Items {
		globalSettings = append(globalSettings, gs.Name)
	}

	return globalSettings, nil
}

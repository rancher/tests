package globalroles

import (
	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/tests/actions/rbac"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"

	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	webhookErrorMessagePrefix = `admission webhook "rancher.cattle.io.globalroles.management.cattle.io" denied the request: globalrole: Forbidden:`
)

var (
	customGlobalRole = func() *v3.GlobalRole {
		return &v3.GlobalRole{
			ObjectMeta: metav1.ObjectMeta{
				Name: namegen.AppendRandomString("custom-global-role-"),
			},
			Rules: []rbacv1.PolicyRule{
				{
					Verbs:     []string{"get", "list", "watch"},
					APIGroups: []string{"*"},
					Resources: []string{"*"},
				},
			},
		}
	}
)

func createUserWithBuiltinRole(client *rancher.Client, builtinGlobalRole rbac.Role) (*management.User, error) {
	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), builtinGlobalRole.String())
	if err != nil {
		return nil, err
	}

	return createdUser, err
}

func createCustomGlobalRoleAndUser(client *rancher.Client, globalRole *v3.GlobalRole) (*v3.GlobalRole, *management.User, error) {
	createdGlobalRole, err := extrbacapi.CreateGlobalRole(client, globalRole)
	if err != nil {
		return nil, nil, err
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String(), createdGlobalRole.Name)
	if err != nil {
		return nil, nil, err
	}

	return createdGlobalRole, createdUser, err
}

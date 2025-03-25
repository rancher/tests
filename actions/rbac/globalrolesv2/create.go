package globalrolesv2

import (
	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"

	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/tests/actions/kubeapi/namespaces"
	"github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"

	"github.com/rancher/shepherd/extensions/users"
	rbacv1 "k8s.io/api/rbac/v1"
)

func CreateGlobalRoleWithInheritedClusterRoles(client *rancher.Client, inheritedRoles []string) (*v3.GlobalRole, error) {
	GlobalRole.Name = namegen.AppendRandomString("testgr")
	GlobalRole.InheritedClusterRoles = inheritedRoles
	createdGlobalRole, err := client.WranglerContext.Mgmt.GlobalRole().Create(&GlobalRole)
	if err != nil {
		return nil, err
	}

	return createdGlobalRole, nil
}

func CreateProjectAndAddANamespace(client *rancher.Client, nsPrefix string) (string, error) {
	project := projects.NewProjectTemplate(Localcluster)
	customProject, err := client.WranglerContext.Mgmt.Project().Create(project)
	if err != nil {
		return "", err
	}
	customNS1, err := namespaces.CreateNamespace(client, Localcluster, customProject.Name, namegen.AppendRandomString(nsPrefix), "", nil, nil)
	return customNS1.Name, err
}

func CreateGlobalRoleWithNamespacedRules(client *rancher.Client, namespacedRules map[string][]rbacv1.PolicyRule) (*v3.GlobalRole, error) {
	GlobalRole.Name = namegen.AppendRandomString("test-nsr")
	GlobalRole.NamespacedRules = namespacedRules
	createdGlobalRole, err := rbacapi.CreateGlobalRole(client, &GlobalRole)
	if err != nil {
		return nil, err
	}
	return createdGlobalRole, nil
}

func CreateGlobalRoleAndUser(client *rancher.Client, inheritedClusterrole []string) (*management.User, error) {
	globalRole, err := CreateGlobalRoleWithInheritedClusterRoles(client, inheritedClusterrole)
	if err != nil {
		return nil, err
	}

	createdUser, err := users.CreateUserWithRole(client, users.UserConfig(), rbac.StandardUser.String(), globalRole.Name)
	if err != nil {
		return nil, err
	}

	return createdUser, err
}

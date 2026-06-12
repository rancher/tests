package rbac

import (
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
)

// UpdateRoleTemplateInheritance updates the inheritance of a role template using wrangler context
func UpdateRoleTemplateInheritance(client *rancher.Client, roleTemplateName string, inheritedRoles []*v3.RoleTemplate) (*v3.RoleTemplate, error) {
	var roleTemplateNames []string
	for _, inheritedRole := range inheritedRoles {
		if inheritedRole != nil {
			roleTemplateNames = append(roleTemplateNames, inheritedRole.Name)
		}
	}

	existingRoleTemplate, err := GetRoleTemplateByName(client, roleTemplateName)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing RoleTemplate: %w", err)
	}

	existingRoleTemplate.RoleTemplateNames = roleTemplateNames

	return extrbacapi.UpdateRoleTemplate(client, existingRoleTemplate)
}

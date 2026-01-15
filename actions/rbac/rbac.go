package rbac

import (
	"fmt"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
)

type Role string

const (
	Admin                      Role = "admin"
	BaseUser                   Role = "user-base"
	StandardUser               Role = "user"
	ClusterOwner               Role = "cluster-owner"
	ClusterMember              Role = "cluster-member"
	ProjectOwner               Role = "project-owner"
	ProjectMember              Role = "project-member"
	CreateNS                   Role = "create-ns"
	ReadOnly                   Role = "read-only"
	CustomManageProjectMember  Role = "projectroletemplatebindings-manage"
	CrtbView                   Role = "clusterroletemplatebindings-view"
	PrtbView                   Role = "projectroletemplatebindings-view"
	ProjectsCreate             Role = "projects-create"
	ProjectsView               Role = "projects-view"
	ManageWorkloads            Role = "workloads-manage"
	ManageUsers                Role = "users-manage"
	ManageNodes                Role = "nodes-manage"
	ManageConfigMaps           Role = "configmaps-manage"
	SecretsView                Role = "secrets-view"
	UserKind                        = "User"
	ActiveStatus                    = "active"
	ForbiddenError                  = "403 Forbidden"
	RancherDeploymentNamespace      = "cattle-system"
	DefaultNamespace                = "fleet-default"
	RancherDeploymentName           = "rancher"
	CattleResyncEnvVarName          = "CATTLE_RESYNC_DEFAULT"
	ImageName                       = "nginx"
	GrbOwnerLabel                   = "authz.management.cattle.io/grb-owner"
	GlobalDataNS                    = "cattle-global-data"
	PSALabelKey                     = "pod-security.kubernetes.io/"
	PSAEnforceLabelKey              = "pod-security.kubernetes.io/enforce"
	PSAWarnLabelKey                 = "pod-security.kubernetes.io/warn"
	PSAAuditLabelKey                = "pod-security.kubernetes.io/audit"
	PSAPrivilegedPolicy             = "privileged"
	PSABaselinePolicy               = "baseline"
	PSARestrictedPolicy             = "restricted"
	PSAEnforceVersionLabelKey       = "pod-security.kubernetes.io/enforce-version"
	PSAWarnVersionLabelKey          = "pod-security.kubernetes.io/warn-version"
	PSAAuditVersionLabelKey         = "pod-security.kubernetes.io/audit-version"
	PSALatestValue                  = "latest"
	CrtbOwnerLabel                  = "authz.cluster.cattle.io/crtb-owner"
	PrtbOwnerLabel                  = "authz.cluster.cattle.io/prtb-owner"
	ClusterNameAnnotationKey        = "cluster.cattle.io/name"
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

	roleContext, err := rbacapi.GetRoleTemplateContext(client, role)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to get context for role %s: %w", role, err)
	}

	switch roleContext {
	case rbacapi.ProjectContext:
		if project == nil {
			return nil, nil, fmt.Errorf("project is required for project-scoped role: %s", role)
		}
		_, err = rbacapi.CreateProjectRoleTemplateBinding(client, standardUser, project, role)
		if err != nil {
			return nil, nil, err
		}
	case rbacapi.ClusterContext:
		if cluster == nil {
			return nil, nil, fmt.Errorf("cluster is required for cluster-scoped role: %s", role)
		}
		_, err = rbacapi.CreateClusterRoleTemplateBinding(client, cluster.ID, standardUser, role)
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

package rbac

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	apiV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/wrangler"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespacesapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectsapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/namespaces"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coreV1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

// VerifyGlobalRoleBindingsForUser validates that a global role bindings is created for a user when the user is created
func VerifyGlobalRoleBindingsForUser(t *testing.T, user *management.User, adminClient *rancher.Client) {
	query := url.Values{"filter": {"userName=" + user.ID}}
	grbs, err := adminClient.Steve.SteveType("management.cattle.io.globalrolebinding").List(query)
	require.NoError(t, err)
	assert.Equal(t, 1, len(grbs.Data))
}

// VerifyRoleBindingsForUser validates that the corresponding role bindings are created for the user
func VerifyRoleBindingsForUser(t *testing.T, user *management.User, adminClient *rancher.Client, clusterID string, role Role, expectedCount int) {
	rblist, err := rbacapi.ListRoleBindings(adminClient, LocalCluster, clusterID, metav1.ListOptions{})
	require.NoError(t, err)
	userID := user.Resource.ID
	userRoleBindings := []string{}

	for _, rb := range rblist.Items {
		if rb.Subjects[0].Kind == UserKind && rb.Subjects[0].Name == userID {
			if rb.RoleRef.Name == role.String() {
				userRoleBindings = append(userRoleBindings, rb.Name)
			}
		}
	}
	assert.Equal(t, expectedCount, len(userRoleBindings))
}

// VerifyUserCanListCluster validates a user with the required global permissions are able to/not able to list the clusters in rancher server
func VerifyUserCanListCluster(t *testing.T, client, standardClient *rancher.Client, clusterID string, role Role) {
	clusterList, err := standardClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
	require.NoError(t, err)

	clusterStatus := &apiV1.ClusterStatus{}
	err = v1.ConvertToK8sType(clusterList.Data[0].Status, clusterStatus)
	require.NoError(t, err)

	assert.Equal(t, 1, len(clusterList.Data))
	actualClusterID := clusterStatus.ClusterName
	assert.Equal(t, clusterID, actualClusterID)
}

// VerifyUserCanListProject validates a user with the required cluster permissions are able/not able to list projects in the downstream cluster
func VerifyUserCanListProject(t *testing.T, client, standardClient *rancher.Client, clusterID, adminProjectName string, role Role) {
	projectListAdmin, err := client.WranglerContext.Mgmt.Project().List(clusterID, metav1.ListOptions{})
	require.NoError(t, err)

	projectListNonAdmin, err := standardClient.WranglerContext.Mgmt.Project().List(clusterID, metav1.ListOptions{})
	switch role {
	case ClusterOwner:
		assert.NoError(t, err)
		assert.Equal(t, len(projectListAdmin.Items), len(projectListNonAdmin.Items))
	case ClusterMember, ProjectOwner, ProjectMember:
		assert.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanGetProject validates a user with the required cluster permissions are able/not able to get the specific project in the downstream cluster
func VerifyUserCanGetProject(t *testing.T, client, standardClient *rancher.Client, clusterID, adminProjectName string, role Role) {
	projectListAdmin, err := client.WranglerContext.Mgmt.Project().Get(clusterID, adminProjectName, metav1.GetOptions{})
	require.NoError(t, err)

	projectListNonAdmin, err := standardClient.WranglerContext.Mgmt.Project().Get(clusterID, adminProjectName, metav1.GetOptions{})
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		assert.NoError(t, err)
		assert.Equal(t, projectListAdmin.Name, projectListNonAdmin.Name)
	case ClusterMember:
		assert.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanCreateProjects validates a user with the required cluster permissions are able/not able to create projects in the downstream cluster
func VerifyUserCanCreateProjects(t *testing.T, client, standardClient *rancher.Client, clusterID string, role Role) {
	projectTemplate := projectsapi.NewProjectTemplate(clusterID)
	memberProject, err := standardClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
	switch role {
	case ClusterOwner, ClusterMember:
		require.NoError(t, err)
		log.Info("Created project as a ", role, " is ", memberProject.Name)
	case ProjectOwner, ProjectMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanCreateNamespace validates a user with the required cluster permissions are able/not able to create namespaces in the project they do not own
func VerifyUserCanCreateNamespace(t *testing.T, client, standardClient *rancher.Client, project *v3.Project, clusterID string, role Role) {
	standardClient, err := standardClient.ReLogin()
	require.NoError(t, err)

	namespaceName := namegen.AppendRandomString("testns-")
	createdNamespace, checkErr := namespacesapi.CreateNamespace(standardClient, clusterID, project.Name, namespaceName, "", map[string]string{}, map[string]string{})

	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, checkErr)
		log.Info("Created a namespace as role ", role, createdNamespace.Name)
		assert.Equal(t, namespaceName, createdNamespace.Name)

		namespaceStatus := &coreV1.NamespaceStatus{}
		err = v1.ConvertToK8sType(createdNamespace.Status, namespaceStatus)
		require.NoError(t, err)
		actualStatus := fmt.Sprintf("%v", namespaceStatus.Phase)
		assert.Equal(t, ActiveStatus, strings.ToLower(actualStatus))
	case ClusterMember:
		require.Error(t, checkErr)
		assert.True(t, apierrors.IsForbidden(checkErr))
	}
}

// VerifyUserCanListNamespace validates a user with the required cluster permissions are able/not able to list namespaces in the project they do not own
func VerifyUserCanListNamespace(t *testing.T, client, standardClient *rancher.Client, project *v3.Project, clusterID string, role Role) {
	log.Info("Validating if ", role, " can lists all namespaces in a cluster.")

	steveAdminClient, err := client.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)
	steveStandardClient, err := standardClient.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	namespaceListAdmin, err := steveAdminClient.SteveType(namespaces.NamespaceSteveType).List(nil)
	require.NoError(t, err)
	sortedNamespaceListAdmin := namespaceListAdmin.Names()

	namespaceListNonAdmin, err := steveStandardClient.SteveType(namespaces.NamespaceSteveType).List(nil)
	require.NoError(t, err)
	sortedNamespaceListNonAdmin := namespaceListNonAdmin.Names()

	switch role {
	case ClusterOwner:
		require.NoError(t, err)
		assert.Equal(t, len(sortedNamespaceListAdmin), len(sortedNamespaceListNonAdmin))
		assert.Equal(t, sortedNamespaceListAdmin, sortedNamespaceListNonAdmin)
	case ClusterMember:
		require.NoError(t, err)
		assert.Equal(t, 0, len(sortedNamespaceListNonAdmin))
	case ProjectOwner, ProjectMember:
		require.NoError(t, err)
		assert.NotEqual(t, len(sortedNamespaceListAdmin), len(sortedNamespaceListNonAdmin))
		assert.Equal(t, 1, len(sortedNamespaceListNonAdmin))
	}
}

// VerifyUserCanDeleteNamespace validates a user with the required cluster permissions are able/not able to delete namespaces in the project they do not own
func VerifyUserCanDeleteNamespace(t *testing.T, client, standardClient *rancher.Client, project *v3.Project, clusterID string, role Role) {

	log.Info("Validating if ", role, " cannot delete a namespace from a project they own.")
	steveAdminClient, err := client.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)
	steveStandardClient, err := standardClient.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	namespaceName := namegen.AppendRandomString("testns-")
	adminNamespace, err := namespacesapi.CreateNamespace(client, clusterID, project.Name, namespaceName+"-admin", "", map[string]string{}, map[string]string{})
	require.NoError(t, err)

	namespaceID, err := steveAdminClient.SteveType(namespaces.NamespaceSteveType).ByID(adminNamespace.Name)
	require.NoError(t, err)
	err = steveStandardClient.SteveType(namespaces.NamespaceSteveType).Delete(namespaceID)

	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
	case ClusterMember:
		require.Error(t, err)
		assert.Equal(t, err.Error(), "Resource type [namespace] can not be deleted")
	}
}

// VerifyUserCanAddClusterRoles validates a user with the required cluster permissions are able/not able to add other users in the cluster
func VerifyUserCanAddClusterRoles(t *testing.T, client, memberClient *rancher.Client, cluster *management.Cluster, role Role) {
	additionalClusterUser, err := users.CreateUserWithRole(client, users.UserConfig(), StandardUser.String())
	require.NoError(t, err)

	_, errUserRole := CreateClusterRoleTemplateBinding(memberClient, cluster.ID, additionalClusterUser, ClusterOwner.String())
	switch role {
	case ProjectOwner, ProjectMember:
		require.Error(t, errUserRole)
		assert.True(t, apierrors.IsForbidden(errUserRole))
	}
}

// VerifyUserCanAddProjectRoles validates a user with the required cluster permissions are able/not able to add other users in a project on the downstream cluster
func VerifyUserCanAddProjectRoles(t *testing.T, client *rancher.Client, project *v3.Project, additionalUser *management.User, projectRole, clusterID string, role Role) {

	_, errUserRole := CreateProjectRoleTemplateBinding(client, additionalUser, project, projectRole)
	switch role {
	case ProjectOwner:
		require.NoError(t, errUserRole)
	case ProjectMember:
		require.Error(t, errUserRole)
	}
}

// VerifyUserCanDeleteProject validates a user with the required cluster/project permissions are able/not able to delete projects in the downstream cluster
func VerifyUserCanDeleteProject(t *testing.T, client *rancher.Client, project *v3.Project, role Role) {
	err := client.WranglerContext.Mgmt.Project().Delete(project.Namespace, project.Name, &metav1.DeleteOptions{})
	switch role {
	case ClusterOwner, ProjectOwner:
		require.NoError(t, err)
	case ClusterMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	case ProjectMember:
		require.Error(t, err)
	}
}

// VerifyUserCanRemoveClusterRoles validates a user with the required cluster/project permissions are able/not able to remove cluster roles in the downstream cluster
func VerifyUserCanRemoveClusterRoles(t *testing.T, client *rancher.Client, user *management.User) {
	err := users.RemoveClusterRoleFromUser(client, user)
	require.NoError(t, err)
}

// VerifyClusterRoleTemplateBindingForUser is a helper function to verify the number of cluster role template bindings for a user
func VerifyClusterRoleTemplateBindingForUser(client *rancher.Client, username string, expectedCount int) ([]v3.ClusterRoleTemplateBinding, error) {
	crtbList, err := rbacapi.ListClusterRoleTemplateBindings(client, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ClusterRoleTemplateBindings: %w", err)
	}

	userCrtbs := []v3.ClusterRoleTemplateBinding{}
	actualCount := 0
	for _, crtb := range crtbList.Items {
		if crtb.UserName == username {
			userCrtbs = append(userCrtbs, crtb)
			actualCount++
		}
	}

	if actualCount != expectedCount {
		return nil, fmt.Errorf("expected %d ClusterRoleTemplateBindings for user %s, but found %d",
			expectedCount, username, actualCount)
	}

	return userCrtbs, nil
}

// VerifyProjectRoleTemplateBindingForUser is a helper function to verify the number of project role template bindings for a user
func VerifyProjectRoleTemplateBindingForUser(client *rancher.Client, username string, expectedCount int) ([]v3.ProjectRoleTemplateBinding, error) {
	prtbList, err := rbacapi.ListProjectRoleTemplateBindings(client, metav1.ListOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to list ProjectRoleTemplateBindings: %w", err)
	}

	userPrtbs := []v3.ProjectRoleTemplateBinding{}
	actualCount := 0
	for _, prtb := range prtbList.Items {
		if prtb.UserName == username {
			userPrtbs = append(userPrtbs, prtb)
			actualCount++
		}
	}

	if actualCount != expectedCount {
		return nil, fmt.Errorf("expected %d ProjectRoleTemplateBindings for user %s, but found %d",
			expectedCount, username, actualCount)
	}

	return userPrtbs, nil
}

// VerifyUserPermission validates that a user has the expected permissions for a given resource
func VerifyUserPermission(client *rancher.Client, clusterID string, user *management.User, verb, resourceType, namespaceName, resourceName string, expected, isCRDInLocalCluster bool) error {
	allowed, err := CheckUserAccess(client, clusterID, user, verb, resourceType, namespaceName, resourceName, isCRDInLocalCluster)

	if expected {
		if err != nil {
			if apierrors.IsForbidden(err) {
				return fmt.Errorf("user should have '%s' access to %s/%s/%s, but got forbidden error: %v", verb, resourceType, namespaceName, resourceName, err)
			}
			return fmt.Errorf("error verifying user access to %s/%s/%s: %v", resourceType, namespaceName, resourceName, err)
		}
		if !allowed {
			return fmt.Errorf("user should have '%s' access to %s/%s/%s, but access was denied", verb, resourceType, namespaceName, resourceName)
		}
	} else {
		if err == nil && allowed {
			return fmt.Errorf("expected '%s' access to %s/%s/%s to be denied, but access was granted", verb, resourceType, namespaceName, resourceName)
		}
		if err != nil && !apierrors.IsForbidden(err) {
			return fmt.Errorf("expected forbidden error for %s/%s/%s, but got: %v", resourceType, namespaceName, resourceName, err)
		}
	}

	return nil
}

// CheckUserAccess checks if a user has the specified access to a resource in a cluster. It returns true if the user has access, false otherwise.
func CheckUserAccess(client *rancher.Client, clusterID string, user *management.User, verb, resourceType, namespaceName, resourceName string, isCRDInLocalCluster bool) (bool, error) {
	userClient, err := client.AsUser(user)
	if err != nil {
		return false, fmt.Errorf("failed to create user client: %w", err)
	}

	var userContext *wrangler.Context
	if isCRDInLocalCluster {
		userContext, err = clusterapi.GetClusterWranglerContext(userClient, rbacapi.LocalCluster)
	} else {
		userContext, err = clusterapi.GetClusterWranglerContext(userClient, clusterID)
	}

	if err != nil {
		return false, fmt.Errorf("failed to get user context: %w", err)
	}

	switch resourceType {
	case "projects":
		return CheckProjectAccess(userContext, verb, clusterID, resourceName)
	case "namespaces":
		return CheckNamespaceAccess(userContext, verb, resourceName)
	case "deployments":
		return CheckDeploymentAccess(userContext, verb, namespaceName, resourceName)
	case "pods":
		return CheckPodAccess(userContext, verb, namespaceName, resourceName)
	case "secrets":
		return CheckSecretAccess(userContext, verb, namespaceName, resourceName)
	case "projectroletemplatebindings":
		return CheckPrtbAccess(userContext, verb, namespaceName, resourceName)
	case "configmaps":
		return CheckConfigMapAccess(userContext, verb, namespaceName, resourceName)
	default:
		return false, fmt.Errorf("checks for resource type '%s' not added", resourceType)
	}
}

// CheckProjectAccess checks if a user has the specified access to a project in a cluster. It returns true if the user has access, false otherwise.
func CheckProjectAccess(userContext *wrangler.Context, verb, clusterID, projectName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Mgmt.Project().List(clusterID, metav1.ListOptions{})
		return err == nil, err
	case "create":
		projectTemplate := projectsapi.NewProjectTemplate(clusterID)
		_, err := userContext.Mgmt.Project().Create(projectTemplate)
		return err == nil, err
	case "delete":
		err := userContext.Mgmt.Project().Delete(clusterID, projectName, &metav1.DeleteOptions{})
		return err == nil, err
	case "update":
		project, err := userContext.Mgmt.Project().Get(clusterID, projectName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if project.Labels == nil {
			project.Labels = make(map[string]string)
		}
		project.Labels["hello"] = "world"
		_, err = userContext.Mgmt.Project().Update(project)
		return err == nil, err
	case "patch":
		patchData := []byte(`{"metadata":{"annotations":{"patched":"true"}}}`)
		_, err := userContext.Mgmt.Project().Patch(clusterID, projectName, types.MergePatchType, patchData)
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for projects", verb)
	}
}

// CheckNamespaceAccess checks if a user has the specified access to a namespace in a cluster. It returns true if the user has access, false otherwise.
func CheckNamespaceAccess(userContext *wrangler.Context, verb, namespaceName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.Namespace().Get(namespaceName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.Namespace().List(metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.Namespace().Delete(namespaceName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'namespaces'", verb)
	}
}

// CheckPodAccess checks if a user has the specified access to a pod in a namespace. It returns true if the user has access, false otherwise.
func CheckPodAccess(userContext *wrangler.Context, verb, namespaceName, podName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.Pod().Get(namespaceName, podName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.Pod().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.Pod().Delete(namespaceName, podName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'pods'", verb)
	}
}

// CheckDeploymentAccess checks if a user has the specified access to a deployment in a namespace. It returns true if the user has access, false otherwise.
func CheckDeploymentAccess(userContext *wrangler.Context, verb, namespaceName, deploymentName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Apps.Deployment().Get(namespaceName, deploymentName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Apps.Deployment().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Apps.Deployment().Delete(namespaceName, deploymentName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'deployments'", verb)
	}
}

// CheckSecretAccess checks if a user has the specified access to a secret in a namespace. It returns true if the user has access, false otherwise.
func CheckSecretAccess(userContext *wrangler.Context, verb, namespaceName, secretName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.Secret().Get(namespaceName, secretName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.Secret().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.Secret().Delete(namespaceName, secretName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'namespaces'", verb)
	}
}

// CheckPrtbAccess checks if a user has the specified access to a project role template binding in a namespace. It returns true if the user has access, false otherwise.
func CheckPrtbAccess(userContext *wrangler.Context, verb, prtbNamespace, prtbName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbNamespace, prtbName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().List(prtbNamespace, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Mgmt.ProjectRoleTemplateBinding().Delete(prtbNamespace, prtbName, &metav1.DeleteOptions{})
		return err == nil, err
	case "update":
		prtb, err := userContext.Mgmt.ProjectRoleTemplateBinding().Get(prtbNamespace, prtbName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if prtb.Labels == nil {
			prtb.Labels = make(map[string]string)
		}
		prtb.Labels["hello"] = "world"
		_, err = userContext.Mgmt.ProjectRoleTemplateBinding().Update(prtb)
		return err == nil, err
	case "patch":
		patchData := []byte(`{"metadata":{"annotations":{"patched":"true"}}}`)
		_, err := userContext.Mgmt.ProjectRoleTemplateBinding().Patch(prtbNamespace, prtbName, types.MergePatchType, patchData)
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for prtbs", verb)
	}
}

// CheckConfigMapAccess checks if a user has the specified access to a ConfigMap in a namespace. It returns true if the user has access, false otherwise.
func CheckConfigMapAccess(userContext *wrangler.Context, verb, namespaceName, configMapName string) (bool, error) {
	switch verb {
	case "get":
		_, err := userContext.Core.ConfigMap().Get(namespaceName, configMapName, metav1.GetOptions{})
		return err == nil, err
	case "list":
		_, err := userContext.Core.ConfigMap().List(namespaceName, metav1.ListOptions{})
		return err == nil, err
	case "delete":
		err := userContext.Core.ConfigMap().Delete(namespaceName, configMapName, &metav1.DeleteOptions{})
		return err == nil, err
	default:
		return false, fmt.Errorf("verb '%s' not available in checks for resource 'configmaps'", verb)
	}
}

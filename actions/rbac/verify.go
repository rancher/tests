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

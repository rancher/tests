package rbac

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	apiV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	rbacaction "github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coreV1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type Role string

const (
	restrictedAdmin rbacaction.Role = "restricted-admin"
)

// verifyRAGlobalRoleBindingsForUser validates that a global role bindings is created for a user when the user is created
func verifyRAGlobalRoleBindingsForUser(t *testing.T, user *management.User, adminClient *rancher.Client) {
	query := url.Values{"filter": {"userName=" + user.ID}}
	grbs, err := adminClient.Steve.SteveType("management.cattle.io.globalrolebinding").List(query)
	require.NoError(t, err)
	assert.Equal(t, 1, len(grbs.Data))
}

// verifyRARoleBindingsForUser validates that the corresponding role bindings are created for the user
func verifyRARoleBindingsForUser(t *testing.T, user *management.User, adminClient *rancher.Client, clusterID string) {
	rblist, err := rbacapi.ListRoleBindings(adminClient, rbacaction.LocalCluster, clusterID, metav1.ListOptions{})
	require.NoError(t, err)
	userID := user.Resource.ID
	userRoleBindings := []string{}

	for _, rb := range rblist.Items {
		if rb.Subjects[0].Kind == rbacaction.UserKind && rb.Subjects[0].Name == userID {
			userRoleBindings = append(userRoleBindings, rb.Name)
		}
	}

	assert.Equal(t, 2, len(userRoleBindings))
}

// verifyRAUserCanListCluster validates a user with the required global permissions are able to/not able to list the clusters in rancher server
func verifyRAUserCanListCluster(t *testing.T, client, standardClient *rancher.Client, clusterID string, role rbacaction.Role) {
	clusterList, err := standardClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
	require.NoError(t, err)

	clusterStatus := &apiV1.ClusterStatus{}
	err = v1.ConvertToK8sType(clusterList.Data[0].Status, clusterStatus)
	require.NoError(t, err)

	if role == restrictedAdmin {
		adminClusterList, err := client.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
		require.NoError(t, err)
		assert.Equal(t, (len(adminClusterList.Data) - 1), len(clusterList.Data))
	}
	assert.Equal(t, 1, len(clusterList.Data))
	actualClusterID := clusterStatus.ClusterName
	assert.Equal(t, clusterID, actualClusterID)

}

// verifyRAUserCanListProject validates a user with the required cluster permissions are able/not able to list projects in the downstream cluster
func verifyRAUserCanListProject(t *testing.T, client, standardClient *rancher.Client, clusterID string) {
	projectListNonAdmin, err := projects.ListProjectNames(standardClient, clusterID)
	require.NoError(t, err)
	projectListAdmin, err := projects.ListProjectNames(client, clusterID)
	require.NoError(t, err)

	assert.Equal(t, len(projectListAdmin), len(projectListNonAdmin))
	assert.Equal(t, projectListAdmin, projectListNonAdmin)
}

// verifyRAUserCanCreateProjects validates a user with the required cluster permissions are able/not able to create projects in the downstream cluster
func verifyRAUserCanCreateProjects(t *testing.T, standardClient *rancher.Client, clusterID string, role rbacaction.Role) {
	memberProject, err := standardClient.Management.Project.Create(projects.NewProjectConfig(clusterID))

	require.NoError(t, err)
	log.Info("Created project as a ", role, " is ", memberProject.Name)
	actualStatus := fmt.Sprintf("%v", memberProject.State)
	assert.Equal(t, rbacaction.ActiveStatus, strings.ToLower(actualStatus))
}

// verifyRAUserCanCreateNamespace validates a user with the required cluster permissions are able/not able to create namespaces in the project they do not own
func verifyRAUserCanCreateNamespace(t *testing.T, standardClient *rancher.Client, project *management.Project, role rbacaction.Role) {
	namespaceName := namegen.AppendRandomString("testns-")
	standardClient, err := standardClient.ReLogin()
	require.NoError(t, err)

	createdNamespace, checkErr := namespaces.CreateNamespace(standardClient, namespaceName, "{}", map[string]string{}, map[string]string{}, project)

	require.NoError(t, checkErr)
	log.Info("Created a namespace as role ", role, createdNamespace.Name)
	assert.Equal(t, namespaceName, createdNamespace.Name)

	namespaceStatus := &coreV1.NamespaceStatus{}
	err = v1.ConvertToK8sType(createdNamespace.Status, namespaceStatus)
	require.NoError(t, err)
	actualStatus := fmt.Sprintf("%v", namespaceStatus.Phase)
	assert.Equal(t, rbacaction.ActiveStatus, strings.ToLower(actualStatus))

}

// verifyRAUserCanListNamespace validates a user with the required cluster permissions are able/not able to list namespaces in the project they do not own
func verifyRAUserCanListNamespace(t *testing.T, client, standardClient *rancher.Client, clusterID string, role rbacaction.Role) {
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

	require.NoError(t, err)
	assert.Equal(t, len(sortedNamespaceListAdmin), len(sortedNamespaceListNonAdmin))
	assert.Equal(t, sortedNamespaceListAdmin, sortedNamespaceListNonAdmin)
}

// verifyRAUserCanDeleteNamespace validates a user with the required cluster permissions are able/not able to delete namespaces in the project they do not own
func verifyRAUserCanDeleteNamespace(t *testing.T, client, standardClient *rancher.Client, project *management.Project, clusterID string, role rbacaction.Role) {

	log.Info("Validating if ", role, " cannot delete a namespace from a project they own.")
	steveAdminClient, err := client.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)
	steveStandardClient, err := standardClient.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	namespaceName := namegen.AppendRandomString("testns-")
	adminNamespace, err := namespaces.CreateNamespace(client, namespaceName+"-admin", "{}", map[string]string{}, map[string]string{}, project)
	require.NoError(t, err)

	namespaceID, err := steveAdminClient.SteveType(namespaces.NamespaceSteveType).ByID(adminNamespace.ID)
	require.NoError(t, err)
	err = steveStandardClient.SteveType(namespaces.NamespaceSteveType).Delete(namespaceID)

	require.NoError(t, err)
}

// verifyRAUserCanAddClusterRoles validates a user with the required cluster permissions are able/not able to add other users in the cluster
func verifyRAUserCanAddClusterRoles(t *testing.T, client, memberClient *rancher.Client, cluster *management.Cluster) {
	_, _, err := rbac.AddUserWithRoleToCluster(memberClient, rbac.StandardUser.String(), rbac.ClusterOwner.String(), cluster, nil)
	require.NoError(t, err)
}

// verifyRAUserCanAddProjectRoles validates a user with the required cluster permissions are able/not able to add other users in a project on the downstream cluster
func verifyRAUserCanAddProjectRoles(t *testing.T, client *rancher.Client, project *management.Project, additionalUser *management.User, projectRole, clusterID string) {

	errUserRole := users.AddProjectMember(client, project, additionalUser, projectRole, nil)
	projectList, errProjectList := projects.ListProjectNames(client, clusterID)
	require.NoError(t, errProjectList)

	require.NoError(t, errUserRole)
	assert.Contains(t, projectList, project.Name)
}

// verifyRAUserCanRemoveClusterRoles validates a user with the required cluster/project permissions are able/not able to remove cluster roles in the downstream cluster
func verifyRAUserCanRemoveClusterRoles(t *testing.T, client *rancher.Client, user *management.User) {
	err := users.RemoveClusterRoleFromUser(client, user)
	require.NoError(t, err)
}

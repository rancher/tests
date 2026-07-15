package rbac

import (
	"fmt"
	"strings"
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	apiV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	exthpaapi "github.com/rancher/shepherd/extensions/kubeapi/hpa"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	"github.com/rancher/shepherd/extensions/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	hpaapi "github.com/rancher/tests/actions/kubeapi/hpa"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// VerifyGlobalRoleBindingsForUser validates that a global role bindings is created for a user when the user is created
func VerifyGlobalRoleBindingsForUser(t *testing.T, user *management.User, adminClient *rancher.Client) {
	grbList, err := adminClient.WranglerContext.Mgmt.GlobalRoleBinding().List(metav1.ListOptions{})
	require.NoError(t, err)

	count := 0
	for _, grb := range grbList.Items {
		if grb.UserName == user.ID {
			count++
		}
	}
	assert.Equal(t, 1, count)
}

// VerifyRoleBindingsForUser validates that the corresponding role bindings are created for the user
func VerifyRoleBindingsForUser(t *testing.T, user *management.User, adminClient *rancher.Client, clusterID string, role Role, expectedCount int) {
	rblist, err := extrbacapi.ListRoleBindings(adminClient, extclusterapi.LocalCluster, clusterID, metav1.ListOptions{})
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
func VerifyUserCanCreateProjects(t *testing.T, client, standardClient *rancher.Client, standardUser *management.User, clusterID string, role Role) {
	projectTemplate := projectapi.NewProjectTemplate(clusterID)
	if role.String() == ClusterMember.String() {
		projectTemplate.Annotations = map[string]string{
			"field.cattle.io/creatorId": standardUser.ID,
		}
	}
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

	createdNamespace, err := namespaceapi.CreateNamespace(standardClient, clusterID, project.Name, namegen.AppendRandomString("testns"), "", nil, nil)
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
		assert.Equal(t, ActiveStatus, strings.ToLower(string(createdNamespace.Status.Phase)))
	case ClusterMember:
		require.Error(t, err)
		statusErr, ok := err.(*apierrors.StatusError)
		require.Truef(t, ok, "expected a StatusError, got %T: %v", err, err)
		assert.Equal(t, int32(403), statusErr.ErrStatus.Code)
	}
}

// VerifyUserCanListNamespace validates a user with the required cluster permissions are able/not able to list namespaces in the project they do not own
func VerifyUserCanListNamespace(t *testing.T, client, standardClient *rancher.Client, project *v3.Project, clusterID string, role Role) {
	log.Info("Validating if ", role, " can lists all namespaces in a cluster.")

	namespaceListAdmin, err := extnamespaceapi.ListNamespaces(client, clusterID, metav1.ListOptions{})
	require.NoError(t, err)

	namespaceListNonAdmin, err := extnamespaceapi.ListNamespaces(standardClient, clusterID, metav1.ListOptions{})
	switch role {
	case ClusterOwner:
		require.NoError(t, err)
		assert.Equal(t, len(namespaceListAdmin.Items), len(namespaceListNonAdmin.Items))
	case ClusterMember, ProjectOwner, ProjectMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanDeleteNamespace validates a user with the required cluster permissions are able/not able to delete namespaces in the project they do not own
func VerifyUserCanDeleteNamespace(t *testing.T, client, standardClient *rancher.Client, project *v3.Project, clusterID string, role Role) {
	log.Info("Validating if ", role, " cannot delete a namespace from a project they own.")

	adminNamespace, err := namespaceapi.CreateNamespace(client, clusterID, project.Name, namegen.AppendRandomString("testns"), "", nil, nil)
	require.NoError(t, err)

	err = extnamespaceapi.DeleteNamespace(standardClient, clusterID, adminNamespace.Name, false)
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
		err = extnamespaceapi.WaitForNamespaceDeletion(standardClient, clusterID, adminNamespace.Name)
		assert.NoError(t, err)
	case ClusterMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanAddClusterRoles validates a user with the required cluster permissions are able/not able to add other users in the cluster
func VerifyUserCanAddClusterRoles(t *testing.T, client, memberClient *rancher.Client, cluster *management.Cluster, role Role) {
	additionalClusterUser, err := users.CreateUserWithRole(client, users.UserConfig(), StandardUser.String())
	require.NoError(t, err)

	_, errUserRole := rbacapi.CreateClusterRoleTemplateBinding(memberClient, cluster.ID, additionalClusterUser.ID, ClusterOwner.String())
	switch role {
	case ProjectOwner, ProjectMember:
		require.Error(t, errUserRole)
		assert.True(t, apierrors.IsForbidden(errUserRole))
	}
}

// VerifyUserCanAddProjectRoles validates a user with the required cluster permissions are able/not able to add other users in a project on the downstream cluster
func VerifyUserCanAddProjectRoles(t *testing.T, client *rancher.Client, project *v3.Project, additionalUser *management.User, projectRole, clusterID string, role Role) {
	_, errUserRole := rbacapi.CreateProjectRoleTemplateBinding(client, additionalUser.ID, project, projectRole)
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

// VerifyUserCanCreateHPA validates that a user with the given role can or cannot create an HPA in the project namespace
func VerifyUserCanCreateHPA(t *testing.T, client, standardClient *rancher.Client, clusterID, namespaceName string, role Role) {
	log.Info("Validating if ", role, " can create HPA in the project namespace.")
	createdHpa, _, err := hpaapi.CreateHPA(standardClient, clusterID, namespaceName, nil, false)
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
		err = exthpaapi.WaitForHPAActive(standardClient, clusterID, namespaceName, createdHpa.Name, createdHpa.Status.DesiredReplicas)
		assert.NoError(t, err)
	case ClusterMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanEditHPA validates that a user with the given role can or cannot edit an HPA in the project namespace
func VerifyUserCanEditHPA(t *testing.T, client, standardClient *rancher.Client, clusterID, namespaceName string, role Role) {
	log.Info("Validating if ", role, " can edit HPA in the project namespace.")
	createdHPA, workload, err := hpaapi.CreateHPA(client, clusterID, namespaceName, nil, false)
	require.NoError(t, err)

	metrics := []autoscalingv2.MetricSpec{hpaapi.BuildMemoryAverageValueMetric(hpaapi.HpaMemoryAvgValue)}
	updatedMin := int32(3)
	updatedMax := int32(6)
	updatedHPA := hpaapi.NewHPAObject(createdHPA.Name, namespaceName, workload.Name, updatedMin, updatedMax, metrics)

	log.Infof("Updating HPA %s: minReplicas=%d, maxReplicas=%d", createdHPA.Name, updatedMin, updatedMax)
	resultHPA, err := exthpaapi.UpdateHPA(standardClient, clusterID, updatedHPA, false)
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
		err = exthpaapi.WaitForHPAActive(standardClient, clusterID, namespaceName, resultHPA.Name, resultHPA.Status.DesiredReplicas)
		assert.NoError(t, err)
		err = VerifyHPAFields(resultHPA, resultHPA.Name, 3, 6)
		assert.NoError(t, err)
	case ClusterMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanDeleteHPA validates that a user with the given role can or cannot delete an HPA in the project namespace
func VerifyUserCanDeleteHPA(t *testing.T, client, standardClient *rancher.Client, clusterID, namespaceName string, role Role) {
	log.Info("Validating if ", role, " can delete HPA in the project namespace.")
	createdHPA, _, err := hpaapi.CreateHPA(client, clusterID, namespaceName, nil, true)
	require.NoError(t, err)

	err = exthpaapi.DeleteHPA(standardClient, clusterID, namespaceName, createdHPA.Name, false)
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
		err = exthpaapi.WaitForHPADeletion(standardClient, clusterID, namespaceName, createdHPA.Name)
		assert.NoError(t, err)
	case ClusterMember:
		require.Error(t, err)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyUserCanListHPA validates that a user with the given role can or cannot list HPAs in the project namespace
func VerifyUserCanListHPA(t *testing.T, client, standardClient *rancher.Client, clusterID, namespaceName string, role Role) {
	log.Info("Validating if ", role, " can list HPAs in the project namespace.")
	createdHPA, _, err := hpaapi.CreateHPA(client, clusterID, namespaceName, nil, true)
	require.NoError(t, err)

	listOpts := metav1.ListOptions{
		FieldSelector: fmt.Sprintf("metadata.name=%s", createdHPA.Name),
	}
	hpaList, err := exthpaapi.ListHPAs(standardClient, clusterID, namespaceName, listOpts)
	switch role {
	case ClusterOwner, ProjectOwner, ProjectMember:
		require.NoError(t, err)
		assert.Len(t, hpaList.Items, 1)
		assert.Equal(t, createdHPA.Name, hpaList.Names()[0])
	case ClusterMember:
		assert.Empty(t, hpaList)
		assert.True(t, apierrors.IsForbidden(err))
	}
}

// VerifyHPAFields validates the fields of an HPA object.
func VerifyHPAFields(hpa *autoscalingv2.HorizontalPodAutoscaler, expectedName string, expectedMin, expectedMax int32) error {
	if hpa.Name != expectedName {
		return fmt.Errorf("expected HPA name %s, got %s", expectedName, hpa.Name)
	}

	if hpa.Spec.MinReplicas != nil && *hpa.Spec.MinReplicas != expectedMin {
		return fmt.Errorf("expected minReplicas %d, got %d", expectedMin, *hpa.Spec.MinReplicas)
	}

	if hpa.Spec.MaxReplicas != expectedMax {
		return fmt.Errorf("expected maxReplicas %d, got %d", expectedMax, hpa.Spec.MaxReplicas)
	}

	return nil
}

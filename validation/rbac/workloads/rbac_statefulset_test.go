//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package workloads

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extstatefulsetapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/statefulsets"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	statefulsetapi "github.com/rancher/tests/actions/kubeapi/workloads/statefulsets"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RbacStatefulsetTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rs *RbacStatefulsetTestSuite) TearDownSuite() {
	rs.session.Cleanup()
}

func (rs *RbacStatefulsetTestSuite) SetupSuite() {
	rs.session = session.NewSession()

	client, err := rancher.NewClient("", rs.session)
	require.NoError(rs.T(), err)
	rs.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rs")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rs.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rs.client, clusterName)
	require.NoError(rs.T(), err, "Error getting cluster ID")
	rs.cluster, err = rs.client.Management.Cluster.ByID(clusterID)
	require.NoError(rs.T(), err)
}

func (rs *RbacStatefulsetTestSuite) TestCreateStatefulSet() {
	subSession := rs.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rs.Run("Validate statefulset creation as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rs.client, rs.cluster.ID)
			assert.NoError(rs.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rs.client, tt.member, tt.role.String(), rs.cluster, adminProject)
			assert.NoError(rs.T(), err)

			log.Infof("As a %v, creating a statefulset", tt.role.String())
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			_, err = statefulsetapi.CreateStatefulSet(userClient, rs.cluster.ID, namespace.Name, podTemplate, 1, false, "")
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rs.T(), err, "failed to create statefulset")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rs.T(), err)
				assert.True(rs.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rs *RbacStatefulsetTestSuite) TestListStatefulset() {
	subSession := rs.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rs.Run("Validate listing statefulset as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rs.client, rs.cluster.ID)
			assert.NoError(rs.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rs.client, tt.member, tt.role.String(), rs.cluster, adminProject)
			assert.NoError(rs.T(), err)

			log.Infof("As a %v, create a statefulset in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdStatefulset, err := statefulsetapi.CreateStatefulSet(rs.client, rs.cluster.ID, namespace.Name, podTemplate, 1, true, "")
			assert.NoError(rs.T(), err, "failed to create statefulset")

			log.Infof("As a %v, list the statefulset", tt.role.String())
			statefulsetList, err := extstatefulsetapi.ListStatefulSets(userClient, rs.cluster.ID, namespace.Name, metav1.ListOptions{})
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String(), rbac.ReadOnly.String():
				assert.NoError(rs.T(), err, "failed to list statefulset")
				assert.Equal(rs.T(), len(statefulsetList.Items), 1)
				assert.Equal(rs.T(), statefulsetList.Items[0].Name, createdStatefulset.Name)
			case rbac.ClusterMember.String():
				assert.Error(rs.T(), err)
				assert.True(rs.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rs *RbacStatefulsetTestSuite) TestUpdateStatefulset() {
	subSession := rs.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rs.Run("Validate updating statefulset as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rs.client, rs.cluster.ID)
			assert.NoError(rs.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rs.client, tt.member, tt.role.String(), rs.cluster, adminProject)
			assert.NoError(rs.T(), err)

			log.Infof("As a %v, create a statefulset in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdStatefulset, err := statefulsetapi.CreateStatefulSet(rs.client, rs.cluster.ID, namespace.Name, podTemplate, 1, true, "")
			assert.NoError(rs.T(), err, "failed to create statefulset")

			log.Infof("As a %v, update the statefulset %s with a new label.", tt.role.String(), createdStatefulset.Name)
			if createdStatefulset.Labels == nil {
				createdStatefulset.Labels = make(map[string]string)
			}
			createdStatefulset.Labels["updated"] = "true"
			updatedStatefulset, err := extstatefulsetapi.UpdateStatefulSet(userClient, rs.cluster.ID, createdStatefulset, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rs.T(), err, "failed to update statefulset")
				err = extstatefulsetapi.WaitForStatefulSetReady(userClient, rs.cluster.ID, namespace.Name, createdStatefulset.Name)
				assert.NoError(rs.T(), err)
				updatedStatefulset, err = extstatefulsetapi.GetStatefulSetByName(userClient, rs.cluster.ID, namespace.Name, updatedStatefulset.Name)
				assert.NoError(rs.T(), err, "Failed to get the updated statefulset after updating labels.")
				assert.Equal(rs.T(), "true", updatedStatefulset.Labels["updated"], "statefulset label update failed.")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rs.T(), err)
				assert.True(rs.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rs *RbacStatefulsetTestSuite) TestDeleteStatefulset() {
	subSession := rs.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		role   rbac.Role
		member string
	}{
		{rbac.ClusterOwner, rbac.StandardUser.String()},
		{rbac.ClusterMember, rbac.StandardUser.String()},
		{rbac.ProjectOwner, rbac.StandardUser.String()},
		{rbac.ProjectMember, rbac.StandardUser.String()},
		{rbac.ReadOnly, rbac.StandardUser.String()},
	}

	for _, tt := range tests {
		rs.Run("Validate deleting statefulset as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rs.client, rs.cluster.ID)
			assert.NoError(rs.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rs.client, tt.member, tt.role.String(), rs.cluster, adminProject)
			assert.NoError(rs.T(), err)

			log.Infof("As a %v, create a statefulset in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdStatefulset, err := statefulsetapi.CreateStatefulSet(rs.client, rs.cluster.ID, namespace.Name, podTemplate, 1, true, "")
			assert.NoError(rs.T(), err, "failed to create statefulset")

			log.Infof("As a %v, delete the statefulset", tt.role.String())
			err = extstatefulsetapi.DeleteStatefulSet(userClient, rs.cluster.ID, createdStatefulset.Namespace, createdStatefulset.Name, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rs.T(), err, "failed to delete statefulset")
				statefulsetList, err := extstatefulsetapi.ListStatefulSets(userClient, rs.cluster.ID, namespace.Name, metav1.ListOptions{})
				assert.NoError(rs.T(), err)
				assert.Equal(rs.T(), len(statefulsetList.Items), 0)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rs.T(), err)
				assert.True(rs.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rs *RbacStatefulsetTestSuite) TestCrudStatefulsetAsClusterMember() {
	subSession := rs.session.NewSession()
	defer subSession.Cleanup()

	role := rbac.ClusterMember.String()
	log.Info("Creating a standard user and adding them to cluster as a cluster member.")
	user, userClient, err := rbac.AddUserWithRoleToCluster(rs.client, rbac.StandardUser.String(), role, rs.cluster, nil)
	require.NoError(rs.T(), err)

	projectTemplate := projectapi.NewProjectTemplate(rs.cluster.ID)
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": user.ID,
	}
	createdProject, err := projectapi.CreateProjectWithTemplate(userClient, rs.cluster.ID, projectTemplate)
	require.NoError(rs.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(userClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(rs.T(), err)

	namespace, err := namespaceapi.CreateNamespace(userClient, rs.cluster.ID, createdProject.Name, namegen.AppendRandomString("ns-"), "", nil, nil)
	require.NoError(rs.T(), err)

	log.Infof("As a %v, create a statefulset in the namespace %v", role, namespace.Name)
	podTemplate := podapi.CreateContainerAndPodTemplate("")
	createdStatefulset, err := statefulsetapi.CreateStatefulSet(userClient, rs.cluster.ID, namespace.Name, podTemplate, 1, true, "")
	assert.NoError(rs.T(), err, "failed to create statefulset")

	log.Infof("As a %v, list the statefulset", role)
	statefulsetList, err := extstatefulsetapi.ListStatefulSets(userClient, rs.cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(rs.T(), err, "failed to list statefulset")
	require.Equal(rs.T(), len(statefulsetList.Items), 1)
	require.Equal(rs.T(), statefulsetList.Items[0].Name, createdStatefulset.Name)

	log.Infof("As a %v, update the statefulset %s with a new label.", role, createdStatefulset.Name)
	if createdStatefulset.Labels == nil {
		createdStatefulset.Labels = make(map[string]string)
	}
	createdStatefulset.Labels["updated"] = "true"
	updatedStatefulset, err := extstatefulsetapi.UpdateStatefulSet(userClient, rs.cluster.ID, createdStatefulset, true)
	require.NoError(rs.T(), err, "failed to update statefulset")
	updatedStatefulset, err = extstatefulsetapi.GetStatefulSetByName(userClient, rs.cluster.ID, updatedStatefulset.Namespace, updatedStatefulset.Name)
	require.NoError(rs.T(), err, "Failed to get the updated statefulset after updating labels.")
	require.Equal(rs.T(), "true", updatedStatefulset.Labels["updated"], "statefulset label update failed.")

	log.Infof("As a %v, delete the statefulset", role)
	err = extstatefulsetapi.DeleteStatefulSet(userClient, rs.cluster.ID, updatedStatefulset.Namespace, updatedStatefulset.Name, true)
	require.NoError(rs.T(), err, "failed to delete statefulset")
	statefulsetList, err = extstatefulsetapi.ListStatefulSets(userClient, rs.cluster.ID, namespace.Name, metav1.ListOptions{})
	require.NoError(rs.T(), err)
	require.Equal(rs.T(), len(statefulsetList.Items), 0)
}

func TestRbacStatefulsetTestSuite(t *testing.T) {
	suite.Run(t, new(RbacStatefulsetTestSuite))
}

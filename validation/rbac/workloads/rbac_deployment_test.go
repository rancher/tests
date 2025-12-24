//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package workloads

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RbacDeploymentTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rd *RbacDeploymentTestSuite) TearDownSuite() {
	rd.session.Cleanup()
}

func (rd *RbacDeploymentTestSuite) SetupSuite() {
	rd.session = session.NewSession()

	client, err := rancher.NewClient("", rd.session)
	require.NoError(rd.T(), err)
	rd.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rd")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rd.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rd.client, clusterName)
	require.NoError(rd.T(), err, "Error getting cluster ID")
	rd.cluster, err = rd.client.Management.Cluster.ByID(clusterID)
	require.NoError(rd.T(), err)
}

func (rd *RbacDeploymentTestSuite) TestCreateDeployment() {
	subSession := rd.session.NewSession()
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
		rd.Run("Validate deployment creation as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rd.client, rd.cluster.ID)
			assert.NoError(rd.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rd.client, tt.member, tt.role.String(), rd.cluster, adminProject)
			assert.NoError(rd.T(), err)

			log.Infof("As a %v, create a deployment", tt.role.String())
			_, err = deployment.CreateDeployment(userClient, rd.cluster.ID, namespace.Name, 1, "", "", false, false, false, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rd.T(), err, "failed to create deployment")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rd.T(), err)
				assert.True(rd.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rd *RbacDeploymentTestSuite) TestListDeployment() {
	subSession := rd.session.NewSession()
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
		rd.Run("Validate listing deployment as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rd.client, rd.cluster.ID)
			assert.NoError(rd.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rd.client, tt.member, tt.role.String(), rd.cluster, adminProject)
			assert.NoError(rd.T(), err)

			log.Infof("As a %v, create a deployment in the namespace %v", rbac.Admin, namespace.Name)
			createdDeployment, err := deployment.CreateDeployment(rd.client, rd.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(rd.T(), err, "failed to create deployment")

			log.Infof("As a %v, list the deployment", tt.role.String())
			standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rd.cluster.ID)
			assert.NoError(rd.T(), err)
			deploymentList, err := standardUserContext.Apps.Deployment().List(namespace.Name, metav1.ListOptions{})
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String(), rbac.ReadOnly.String():
				assert.NoError(rd.T(), err, "failed to list deployment")
				assert.Equal(rd.T(), len(deploymentList.Items), 1)
				assert.Equal(rd.T(), deploymentList.Items[0].Name, createdDeployment.Name)
			case rbac.ClusterMember.String():
				assert.Error(rd.T(), err)
				assert.True(rd.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rd *RbacDeploymentTestSuite) TestUpdateDeployment() {
	subSession := rd.session.NewSession()
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
		rd.Run("Validate updating deployment as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rd.client, rd.cluster.ID)
			assert.NoError(rd.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rd.client, tt.member, tt.role.String(), rd.cluster, adminProject)
			assert.NoError(rd.T(), err)

			log.Infof("As a %v, create a deployment in the namespace %v", rbac.Admin, namespace.Name)
			createdDeployment, err := deployment.CreateDeployment(rd.client, rd.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(rd.T(), err, "failed to create deployment")

			log.Infof("As a %v, update the deployment %s with a new label.", tt.role.String(), createdDeployment.Name)
			if createdDeployment.Labels == nil {
				createdDeployment.Labels = make(map[string]string)
			}
			createdDeployment.Labels["updated"] = "true"
			updatedDeployment, err := deployment.UpdateDeployment(userClient, rd.cluster.ID, namespace.Name, createdDeployment, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rd.T(), err, "failed to update deployment")
				standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rd.cluster.ID)
				assert.NoError(rd.T(), err)
				updatedDeployment, err = standardUserContext.Apps.Deployment().Get(namespace.Name, updatedDeployment.Name, metav1.GetOptions{})
				assert.NoError(rd.T(), err, "Failed to get the updated deployment after updating labels.")
				assert.Equal(rd.T(), "true", updatedDeployment.Labels["updated"], "deployment label update failed.")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rd.T(), err)
				assert.True(rd.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rd *RbacDeploymentTestSuite) TestDeleteDeployment() {
	subSession := rd.session.NewSession()
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
		rd.Run("Validate deleting deployment as user with role "+tt.role.String(), func() {
			log.Info("Create a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rd.client, rd.cluster.ID)
			assert.NoError(rd.T(), err)

			log.Infof("Create a standard user and add the user to a cluster/project role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rd.client, tt.member, tt.role.String(), rd.cluster, adminProject)
			assert.NoError(rd.T(), err)

			log.Infof("As a %v, create a deployment in the namespace %v", rbac.Admin, namespace.Name)
			createdDeployment, err := deployment.CreateDeployment(rd.client, rd.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
			assert.NoError(rd.T(), err, "failed to create deployment")

			log.Infof("As a %v, delete the deployment", tt.role.String())
			standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rd.cluster.ID)
			assert.NoError(rd.T(), err)
			err = standardUserContext.Apps.Deployment().Delete(namespace.Name, createdDeployment.Name, &metav1.DeleteOptions{})
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rd.T(), err, "failed to delete deployment")
				deploymentList, err := standardUserContext.Apps.Deployment().List(namespace.Name, metav1.ListOptions{})
				assert.NoError(rd.T(), err)
				assert.Equal(rd.T(), len(deploymentList.Items), 0)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rd.T(), err)
				assert.True(rd.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rd *RbacDeploymentTestSuite) TestCrudDeploymentAsClusterMember() {
	subSession := rd.session.NewSession()
	defer subSession.Cleanup()

	role := rbac.ClusterMember.String()
	log.Info("Creating a standard user and adding them to cluster as a cluster member.")
	user, userClient, err := rbac.AddUserWithRoleToCluster(rd.client, rbac.StandardUser.String(), role, rd.cluster, nil)
	require.NoError(rd.T(), err)

	projectTemplate := projectapi.NewProjectTemplate(rd.cluster.ID)
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": user.ID,
	}
	createdProject, err := userClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
	require.NoError(rd.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(userClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(rd.T(), err)

	namespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, rd.cluster.ID, createdProject.Name, nil)
	require.NoError(rd.T(), err)

	log.Infof("As a %v, create a deployment in the namespace %v", role, namespace.Name)
	createdDeployment, err := deployment.CreateDeployment(userClient, rd.cluster.ID, namespace.Name, 1, "", "", false, false, false, true)
	require.NoError(rd.T(), err, "failed to create deployment")

	log.Infof("As a %v, list the deployment", role)
	standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rd.cluster.ID)
	require.NoError(rd.T(), err)
	deploymentList, err := standardUserContext.Apps.Deployment().List(namespace.Name, metav1.ListOptions{})
	require.NoError(rd.T(), err, "failed to list deployment")
	require.Equal(rd.T(), len(deploymentList.Items), 1)
	require.Equal(rd.T(), deploymentList.Items[0].Name, createdDeployment.Name)

	log.Infof("As a %v, update the deployment %s with a new label.", role, createdDeployment.Name)
	if createdDeployment.Labels == nil {
		createdDeployment.Labels = make(map[string]string)
	}
	createdDeployment.Labels["updated"] = "true"
	updatedDeployment, err := deployment.UpdateDeployment(userClient, rd.cluster.ID, namespace.Name, createdDeployment, true)
	require.NoError(rd.T(), err, "failed to update deployment")
	updatedDeployment, err = standardUserContext.Apps.Deployment().Get(namespace.Name, updatedDeployment.Name, metav1.GetOptions{})
	require.NoError(rd.T(), err, "Failed to get the updated deployment after updating labels.")
	require.Equal(rd.T(), "true", updatedDeployment.Labels["updated"], "deployment label update failed.")

	log.Infof("As a %v, delete the deployment", role)
	err = standardUserContext.Apps.Deployment().Delete(namespace.Name, createdDeployment.Name, &metav1.DeleteOptions{})
	require.NoError(rd.T(), err, "failed to delete deployment")
	deploymentList, err = standardUserContext.Apps.Deployment().List(namespace.Name, metav1.ListOptions{})
	require.NoError(rd.T(), err)
	require.Equal(rd.T(), len(deploymentList.Items), 0)
}

func TestRbacDeploymentTestSuite(t *testing.T) {
	suite.Run(t, new(RbacDeploymentTestSuite))
}

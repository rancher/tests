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
	"github.com/rancher/tests/actions/workloads/job"
	"github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RbacJobTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rj *RbacJobTestSuite) TearDownSuite() {
	rj.session.Cleanup()
}

func (rj *RbacJobTestSuite) SetupSuite() {
	rj.session = session.NewSession()

	client, err := rancher.NewClient("", rj.session)
	require.NoError(rj.T(), err)
	rj.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rj")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rj.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rj.client, clusterName)
	require.NoError(rj.T(), err, "Error getting cluster ID")
	rj.cluster, err = rj.client.Management.Cluster.ByID(clusterID)
	require.NoError(rj.T(), err)
}

func (rj *RbacJobTestSuite) TestCreateJob() {
	subSession := rj.session.NewSession()
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
		rj.Run("Validate job creation as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rj.client, rj.cluster.ID)
			assert.NoError(rj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rj.client, tt.member, tt.role.String(), rj.cluster, adminProject)
			assert.NoError(rj.T(), err)

			log.Infof("As a %v, creating a job", tt.role.String())
			podTemplate := pods.CreateContainerAndPodTemplate()
			_, err = job.CreateJob(userClient, rj.cluster.ID, namespace.Name, podTemplate, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rj.T(), err, "failed to create job")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rj.T(), err)
				assert.True(rj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rj *RbacJobTestSuite) TestListJob() {
	subSession := rj.session.NewSession()
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
		rj.Run("Validate listing job as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rj.client, rj.cluster.ID)
			assert.NoError(rj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rj.client, tt.member, tt.role.String(), rj.cluster, adminProject)
			assert.NoError(rj.T(), err)

			log.Infof("As a %v, creating a job in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := pods.CreateContainerAndPodTemplate()
			createdJob, err := job.CreateJob(rj.client, rj.cluster.ID, namespace.Name, podTemplate, true)
			assert.NoError(rj.T(), err, "failed to create job")

			log.Infof("As a %v, listing the job", tt.role.String())
			standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rj.cluster.ID)
			assert.NoError(rj.T(), err)
			jobList, err := standardUserContext.Batch.Job().List(namespace.Name, metav1.ListOptions{})
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String(), rbac.ReadOnly.String():
				assert.NoError(rj.T(), err, "failed to list job")
				assert.Equal(rj.T(), len(jobList.Items), 1)
				assert.Equal(rj.T(), jobList.Items[0].Name, createdJob.Name)
			case rbac.ClusterMember.String():
				assert.Error(rj.T(), err)
				assert.True(rj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rj *RbacJobTestSuite) TestUpdateJob() {
	subSession := rj.session.NewSession()
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
		rj.Run("Validate updating job as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rj.client, rj.cluster.ID)
			assert.NoError(rj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rj.client, tt.member, tt.role.String(), rj.cluster, adminProject)
			assert.NoError(rj.T(), err)

			log.Infof("As a %v, creating a job in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := pods.CreateContainerAndPodTemplate()
			createdJob, err := job.CreateJob(rj.client, rj.cluster.ID, namespace.Name, podTemplate, true)
			assert.NoError(rj.T(), err, "failed to create job")

			log.Infof("As a %v, updating the job %s with a new label.", tt.role.String(), createdJob.Name)
			adminContext, err := clusterapi.GetClusterWranglerContext(rj.client, rj.cluster.ID)
			assert.NoError(rj.T(), err)
			standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rj.cluster.ID)
			assert.NoError(rj.T(), err)

			latestJob, err := adminContext.Batch.Job().Get(namespace.Name, createdJob.Name, metav1.GetOptions{})
			assert.NoError(rj.T(), err, "Failed to list job.")

			if latestJob.Labels == nil {
				latestJob.Labels = make(map[string]string)
			}
			latestJob.Labels["updated"] = "true"

			_, err = standardUserContext.Batch.Job().Update(latestJob)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rj.T(), err, "failed to update job")
				updatedJob, err := standardUserContext.Batch.Job().Get(namespace.Name, createdJob.Name, metav1.GetOptions{})
				assert.NoError(rj.T(), err, "Failed to list the job after updating labels.")
				assert.Equal(rj.T(), "true", updatedJob.Labels["updated"], "job label update failed.")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rj.T(), err)
				assert.True(rj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rj *RbacJobTestSuite) TestDeleteJob() {
	subSession := rj.session.NewSession()
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
		rj.Run("Validate deleting job as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rj.client, rj.cluster.ID)
			assert.NoError(rj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rj.client, tt.member, tt.role.String(), rj.cluster, adminProject)
			assert.NoError(rj.T(), err)

			log.Infof("As a %v, creating a job in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := pods.CreateContainerAndPodTemplate()
			createdJob, err := job.CreateJob(rj.client, rj.cluster.ID, namespace.Name, podTemplate, true)
			assert.NoError(rj.T(), err, "failed to create job")

			log.Infof("As a %v, deleting the job", tt.role.String())
			err = job.DeleteJob(userClient, rj.cluster.ID, createdJob, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rj.T(), err, "failed to delete job")
				err = job.WaitForDeleteJob(userClient, rj.cluster.ID, createdJob)
				assert.NoError(rj.T(), err)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rj.T(), err)
				assert.True(rj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rj *RbacJobTestSuite) TestCrudJobAsClusterMember() {
	subSession := rj.session.NewSession()
	defer subSession.Cleanup()

	role := rbac.ClusterMember.String()
	log.Info("Creating a standard user and adding them to cluster as a cluster member.")
	user, userClient, err := rbac.AddUserWithRoleToCluster(rj.client, rbac.StandardUser.String(), role, rj.cluster, nil)
	require.NoError(rj.T(), err)

	projectTemplate := projectapi.NewProjectTemplate(rj.cluster.ID)
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": user.ID,
	}
	createdProject, err := userClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
	require.NoError(rj.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(userClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(rj.T(), err)

	namespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, rj.cluster.ID, createdProject.Name, nil)
	require.NoError(rj.T(), err)

	log.Infof("As a %v, creating a job in the namespace %v", role, namespace.Name)
	podTemplate := pods.CreateContainerAndPodTemplate()
	createdJob, err := job.CreateJob(userClient, rj.cluster.ID, namespace.Name, podTemplate, true)
	require.NoError(rj.T(), err, "failed to create job")

	log.Infof("As a %v, list the job", role)
	standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rj.cluster.ID)
	assert.NoError(rj.T(), err)
	jobList, err := standardUserContext.Batch.Job().List(namespace.Name, metav1.ListOptions{})
	require.NoError(rj.T(), err, "failed to list jobs")
	require.Equal(rj.T(), len(jobList.Items), 1)
	require.Equal(rj.T(), jobList.Items[0].Name, createdJob.Name)

	log.Infof("As a %v, update the job %s with a new label.", role, createdJob.Name)
	latestJob, err := standardUserContext.Batch.Job().Get(namespace.Name, createdJob.Name, metav1.GetOptions{})
	assert.NoError(rj.T(), err, "Failed to get the latest job.")

	if latestJob.Labels == nil {
		latestJob.Labels = make(map[string]string)
	}
	latestJob.Labels["updated"] = "true"

	_, err = standardUserContext.Batch.Job().Update(latestJob)
	require.NoError(rj.T(), err, "failed to update job")
	updatedJobList, err := standardUserContext.Batch.Job().List(namespace.Name, metav1.ListOptions{})
	require.NoError(rj.T(), err, "Failed to list the job after updating labels.")
	require.Equal(rj.T(), "true", updatedJobList.Items[0].Labels["updated"], "job label update failed.")

	log.Infof("As a %v, delete the job", role)
	err = job.DeleteJob(userClient, rj.cluster.ID, createdJob, true)
	require.NoError(rj.T(), err, "failed to delete job")
}

func TestRbacJobTestSuite(t *testing.T) {
	suite.Run(t, new(RbacJobTestSuite))
}

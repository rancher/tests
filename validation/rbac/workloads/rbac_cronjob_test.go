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
	"github.com/rancher/tests/actions/workloads/cronjob"
	"github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type RbacCronJobTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rcj *RbacCronJobTestSuite) TearDownSuite() {
	rcj.session.Cleanup()
}

func (rcj *RbacCronJobTestSuite) SetupSuite() {
	rcj.session = session.NewSession()

	client, err := rancher.NewClient("", rcj.session)
	require.NoError(rcj.T(), err)
	rcj.client = client

	log.Info("Getting cluster name from the config file and append cluster details in rcj")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rcj.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rcj.client, clusterName)
	require.NoError(rcj.T(), err, "Error getting cluster ID")
	rcj.cluster, err = rcj.client.Management.Cluster.ByID(clusterID)
	require.NoError(rcj.T(), err)
}

func (rcj *RbacCronJobTestSuite) TestCreateCronJob() {
	subSession := rcj.session.NewSession()
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
		rcj.Run("Validate cronjob creation as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob", tt.role.String())
			podTemplate := pods.CreateContainerAndPodTemplate()
			_, err = cronjob.CreateCronJob(userClient, rcj.cluster.ID, namespace.Name, "*/1 * * * *", podTemplate, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rcj.T(), err, "failed to create cronjob")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rcj.T(), err)
				assert.True(rcj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rcj *RbacCronJobTestSuite) TestListCronJob() {
	subSession := rcj.session.NewSession()
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
		rcj.Run("Validate listing cronjob as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := pods.CreateContainerAndPodTemplate()
			createdCronJob, err := cronjob.CreateCronJob(rcj.client, rcj.cluster.ID, namespace.Name, "*/1 * * * *", podTemplate, true)
			assert.NoError(rcj.T(), err, "failed to create cronjob")

			log.Infof("As a %v, listing the cronjob", tt.role.String())
			standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)
			cronJobList, err := standardUserContext.Batch.CronJob().List(namespace.Name, metav1.ListOptions{})
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String(), rbac.ReadOnly.String():
				assert.NoError(rcj.T(), err, "failed to list cronjob")
				assert.Equal(rcj.T(), len(cronJobList.Items), 1)
				assert.Equal(rcj.T(), cronJobList.Items[0].Name, createdCronJob.Name)
			case rbac.ClusterMember.String():
				assert.Error(rcj.T(), err)
				assert.True(rcj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rcj *RbacCronJobTestSuite) TestUpdateCronJob() {
	subSession := rcj.session.NewSession()
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
		rcj.Run("Validate updating cronjob as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := pods.CreateContainerAndPodTemplate()
			createdCronJob, err := cronjob.CreateCronJob(rcj.client, rcj.cluster.ID, namespace.Name, "*/1 * * * *", podTemplate, true)
			assert.NoError(rcj.T(), err, "failed to create cronjob")

			log.Infof("As a %v, updating the cronjob %s with a new label.", tt.role.String(), createdCronJob.Name)
			adminContext, err := clusterapi.GetClusterWranglerContext(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)
			standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			latestCronJob, err := adminContext.Batch.CronJob().Get(namespace.Name, createdCronJob.Name, metav1.GetOptions{})
			assert.NoError(rcj.T(), err, "Failed to list cronjob.")

			if latestCronJob.Labels == nil {
				latestCronJob.Labels = make(map[string]string)
			}
			latestCronJob.Labels["updated"] = "true"

			_, err = standardUserContext.Batch.CronJob().Update(latestCronJob)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rcj.T(), err, "failed to update cronjob")
				updatedCronJob, err := standardUserContext.Batch.CronJob().Get(namespace.Name, createdCronJob.Name, metav1.GetOptions{})
				assert.NoError(rcj.T(), err, "Failed to list the cronjob after updating labels.")
				assert.Equal(rcj.T(), "true", updatedCronJob.Labels["updated"], "job label update failed.")
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rcj.T(), err)
				assert.True(rcj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rcj *RbacCronJobTestSuite) TestDeleteCronJob() {
	subSession := rcj.session.NewSession()
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
		rcj.Run("Validate deleting cronjob as user with role "+tt.role.String(), func() {
			log.Info("Creating a project and a namespace in the project.")
			adminProject, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := pods.CreateContainerAndPodTemplate()
			createdCronJob, err := cronjob.CreateCronJob(rcj.client, rcj.cluster.ID, namespace.Name, "*/1 * * * *", podTemplate, true)
			assert.NoError(rcj.T(), err, "failed to create cronjob")

			log.Infof("As a %v, deleting the cronjob", tt.role.String())
			err = cronjob.DeleteCronJob(userClient, rcj.cluster.ID, createdCronJob, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rcj.T(), err, "failed to delete cronjob")
				err = cronjob.WaitForDeleteCronJob(userClient, rcj.cluster.ID, createdCronJob)
				assert.NoError(rcj.T(), err)
			case rbac.ClusterMember.String(), rbac.ReadOnly.String():
				assert.Error(rcj.T(), err)
				assert.True(rcj.T(), errors.IsForbidden(err))
			}
		})
	}
}

func (rcj *RbacCronJobTestSuite) TestCrudCronJobAsClusterMember() {
	subSession := rcj.session.NewSession()
	defer subSession.Cleanup()

	role := rbac.ClusterMember.String()
	log.Info("Creating a standard user and adding them to cluster as a cluster member.")
	user, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, rbac.StandardUser.String(), role, rcj.cluster, nil)
	require.NoError(rcj.T(), err)

	projectTemplate := projectapi.NewProjectTemplate(rcj.cluster.ID)
	projectTemplate.Annotations = map[string]string{
		"field.cattle.io/creatorId": user.ID,
	}
	createdProject, err := userClient.WranglerContext.Mgmt.Project().Create(projectTemplate)
	require.NoError(rcj.T(), err)

	err = projectapi.WaitForProjectFinalizerToUpdate(userClient, createdProject.Name, createdProject.Namespace, 2)
	require.NoError(rcj.T(), err)

	namespace, err := namespaceapi.CreateNamespaceUsingWrangler(userClient, rcj.cluster.ID, createdProject.Name, nil)
	require.NoError(rcj.T(), err)

	log.Infof("As a %v, creating a cronjob in the namespace %v", role, namespace.Name)
	podTemplate := pods.CreateContainerAndPodTemplate()
	createdCronJob, err := cronjob.CreateCronJob(userClient, rcj.cluster.ID, namespace.Name, "*/1 * * * *", podTemplate, true)
	require.NoError(rcj.T(), err, "failed to create cronjob")

	log.Infof("As a %v, listing the cronjob", role)
	standardUserContext, err := clusterapi.GetClusterWranglerContext(userClient, rcj.cluster.ID)
	assert.NoError(rcj.T(), err)
	cronJobList, err := standardUserContext.Batch.CronJob().List(namespace.Name, metav1.ListOptions{})
	require.NoError(rcj.T(), err, "failed to list cronjobs")
	require.Equal(rcj.T(), len(cronJobList.Items), 1)
	require.Equal(rcj.T(), cronJobList.Items[0].Name, createdCronJob.Name)

	log.Infof("As a %v, updating the cronjob %s with a new label.", role, createdCronJob.Name)
	latestCronJob, err := standardUserContext.Batch.CronJob().Get(namespace.Name, createdCronJob.Name, metav1.GetOptions{})
	assert.NoError(rcj.T(), err, "Failed to get the latest cronjob.")

	if latestCronJob.Labels == nil {
		latestCronJob.Labels = make(map[string]string)
	}
	latestCronJob.Labels["updated"] = "true"

	_, err = standardUserContext.Batch.CronJob().Update(latestCronJob)
	require.NoError(rcj.T(), err, "failed to update cronjob")
	updatedCronJobList, err := standardUserContext.Batch.CronJob().List(namespace.Name, metav1.ListOptions{})
	require.NoError(rcj.T(), err, "Failed to list the cronjob after updating labels.")
	require.Equal(rcj.T(), "true", updatedCronJobList.Items[0].Labels["updated"], "job label update failed.")

	log.Infof("As a %v, deleting the cronjob", role)
	err = cronjob.DeleteCronJob(userClient, rcj.cluster.ID, createdCronJob, true)
	require.NoError(rcj.T(), err, "failed to delete cronjob")
}

func TestRbacCronJobTestSuite(t *testing.T) {
	suite.Run(t, new(RbacCronJobTestSuite))
}

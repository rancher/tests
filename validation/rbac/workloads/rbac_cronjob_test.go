//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package workloads

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extcronjobsapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/cronjobs"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	cronjobsapi "github.com/rancher/tests/actions/kubeapi/workloads/cronjobs"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	"github.com/rancher/tests/actions/rbac"
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

	log.Info("Getting cluster name from the config file and append cluster details in the struct")
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob", tt.role.String())
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdCronJob, err := cronjobsapi.CreateCronJob(userClient, rcj.cluster.ID, namespace.Name, cronjobsapi.CronJobSchedule, podTemplate, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rcj.T(), err, "failed to create cronjob")
				err = extcronjobsapi.WaitForCronJobActive(userClient, rcj.cluster.ID, createdCronJob.Namespace, createdCronJob.Name)
				assert.NoError(rcj.T(), err, "failed to wait for cronjob to become active")
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdCronJob, err := cronjobsapi.CreateCronJob(rcj.client, rcj.cluster.ID, namespace.Name, cronjobsapi.CronJobSchedule, podTemplate, true)
			assert.NoError(rcj.T(), err, "failed to create cronjob")

			log.Infof("As a %v, listing the cronjob", tt.role.String())
			cronJobList, err := extcronjobsapi.ListCronJobs(userClient, rcj.cluster.ID, createdCronJob.Namespace, metav1.ListOptions{})
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdCronJob, err := cronjobsapi.CreateCronJob(rcj.client, rcj.cluster.ID, namespace.Name, cronjobsapi.CronJobSchedule, podTemplate, true)
			assert.NoError(rcj.T(), err, "failed to create cronjob")

			log.Infof("As a %v, updating the cronjob %s with a new label.", tt.role.String(), createdCronJob.Name)
			latestCronJob, err := extcronjobsapi.GetCronJobByName(rcj.client, rcj.cluster.ID, createdCronJob.Namespace, createdCronJob.Name)
			assert.NoError(rcj.T(), err, "Failed to list cronjob.")

			if latestCronJob.Labels == nil {
				latestCronJob.Labels = make(map[string]string)
			}
			latestCronJob.Labels["updated"] = "true"

			_, err = extcronjobsapi.UpdateCronJob(userClient, rcj.cluster.ID, latestCronJob, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rcj.T(), err, "failed to update cronjob")
				err := extcronjobsapi.WaitForCronJobActive(userClient, rcj.cluster.ID, latestCronJob.Namespace, latestCronJob.Name)
				assert.NoError(rcj.T(), err, "failed to wait for cronjob to become active")
				updatedCronJob, err := extcronjobsapi.GetCronJobByName(userClient, rcj.cluster.ID, latestCronJob.Namespace, latestCronJob.Name)
				assert.NoError(rcj.T(), err, "Failed to get the cronjob after updating labels.")
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
			adminProject, namespace, err := projectapi.CreateProjectAndNamespace(rcj.client, rcj.cluster.ID)
			assert.NoError(rcj.T(), err)

			log.Infof("Creating a standard user and add the user to a cluster/project with role %s", tt.role)
			_, userClient, err := rbac.AddUserWithRoleToCluster(rcj.client, tt.member, tt.role.String(), rcj.cluster, adminProject)
			assert.NoError(rcj.T(), err)

			log.Infof("As a %v, creating a cronjob in the namespace %v", rbac.Admin, namespace.Name)
			podTemplate := podapi.CreateContainerAndPodTemplate("")
			createdCronJob, err := cronjobsapi.CreateCronJob(rcj.client, rcj.cluster.ID, namespace.Name, cronjobsapi.CronJobSchedule, podTemplate, true)
			assert.NoError(rcj.T(), err, "failed to create cronjob")

			log.Infof("As a %v, deleting the cronjob", tt.role.String())
			err = extcronjobsapi.DeleteCronJob(userClient, rcj.cluster.ID, createdCronJob.Namespace, createdCronJob.Name, false)
			switch tt.role.String() {
			case rbac.ClusterOwner.String(), rbac.ProjectOwner.String(), rbac.ProjectMember.String():
				assert.NoError(rcj.T(), err, "failed to delete cronjob")
				err = extcronjobsapi.WaitForCronJobDeletion(userClient, rcj.cluster.ID, createdCronJob.Namespace, createdCronJob.Name)
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
	createdProject, err := projectapi.CreateProjectWithTemplate(userClient, rcj.cluster.ID, projectTemplate)
	require.NoError(rcj.T(), err)

	namespace, err := namespaceapi.CreateNamespace(userClient, rcj.cluster.ID, createdProject.Name, namegen.AppendRandomString("ns-"), "", nil, nil)
	require.NoError(rcj.T(), err)

	log.Infof("As a %v, creating a cronjob in the namespace %v", role, namespace.Name)
	podTemplate := podapi.CreateContainerAndPodTemplate("")
	createdCronJob, err := cronjobsapi.CreateCronJob(userClient, rcj.cluster.ID, namespace.Name, cronjobsapi.CronJobSchedule, podTemplate, true)
	require.NoError(rcj.T(), err, "failed to create cronjob")

	log.Infof("As a %v, listing the cronjob", role)
	cronJobList, err := extcronjobsapi.ListCronJobs(userClient, rcj.cluster.ID, createdCronJob.Namespace, metav1.ListOptions{})
	require.NoError(rcj.T(), err, "failed to list cronjobs")
	require.Equal(rcj.T(), len(cronJobList.Items), 1)
	require.Equal(rcj.T(), cronJobList.Items[0].Name, createdCronJob.Name)

	log.Infof("As a %v, updating the cronjob %s with a new label.", role, createdCronJob.Name)
	latestCronJob, err := extcronjobsapi.GetCronJobByName(userClient, rcj.cluster.ID, createdCronJob.Namespace, createdCronJob.Name)
	assert.NoError(rcj.T(), err, "Failed to get the latest cronjob.")

	if latestCronJob.Labels == nil {
		latestCronJob.Labels = make(map[string]string)
	}
	latestCronJob.Labels["updated"] = "true"

	_, err = extcronjobsapi.UpdateCronJob(userClient, rcj.cluster.ID, latestCronJob, true)
	require.NoError(rcj.T(), err, "failed to update cronjob")
	updatedCronJob, err := extcronjobsapi.GetCronJobByName(userClient, rcj.cluster.ID, latestCronJob.Namespace, latestCronJob.Name)
	require.NoError(rcj.T(), err, "Failed to list the cronjob after updating labels.")
	require.Equal(rcj.T(), "true", updatedCronJob.Labels["updated"], "job label update failed.")

	log.Infof("As a %v, deleting the cronjob", role)
	err = extcronjobsapi.DeleteCronJob(userClient, rcj.cluster.ID, createdCronJob.Namespace, createdCronJob.Name, true)
	require.NoError(rcj.T(), err, "failed to delete cronjob")
}

func TestRbacCronJobTestSuite(t *testing.T) {
	suite.Run(t, new(RbacCronJobTestSuite))
}

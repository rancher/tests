//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package projects

import (
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rancherleader"
	"github.com/rancher/tests/actions/rbac"
	pod "github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RbacTerminatingProjectTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (rtp *RbacTerminatingProjectTestSuite) TearDownSuite() {
	rtp.session.Cleanup()
}

func (rtp *RbacTerminatingProjectTestSuite) SetupSuite() {
	rtp.session = session.NewSession()

	client, err := rancher.NewClient("", rtp.session)
	require.NoError(rtp.T(), err)

	rtp.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(rtp.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(rtp.client, clusterName)
	require.NoError(rtp.T(), err, "Error getting cluster ID")
	rtp.cluster, err = rtp.client.Management.Cluster.ByID(clusterID)
	require.NoError(rtp.T(), err)
}

func (rtp *RbacTerminatingProjectTestSuite) TestUserAdditionToClusterWithTerminatingProjectNamespace() {
	subSession := rtp.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a standard user and the user as cluster owner to the downstream cluster.")
	createdUser, _, err := rbac.AddUserWithRoleToCluster(rtp.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), rtp.cluster, nil)
	require.NoError(rtp.T(), err)
	rtp.T().Logf("Created user: %v", createdUser.Username)

	log.Info("Create a project in the downstream cluster.")
	createdProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(rtp.client, rtp.cluster.ID)
	require.NoError(rtp.T(), err)

	logCaptureStartTime := time.Now()
	log.Info("Simulate a project stuck in terminating state by adding a finalizer to the project.")
	finalizer := append([]string{projectapi.DummyFinalizer}, createdProject.Finalizers...)
	updatedProject, err := projects.UpdateProjectNamespaceFinalizer(rtp.client, createdProject, finalizer)
	require.NoError(rtp.T(), err, "Failed to update finalizer.")
	err = projectapi.WaitForProjectFinalizerToUpdate(rtp.client, createdProject.Name, createdProject.Namespace, 3)
	require.NoError(rtp.T(), err)

	log.Info("Delete the Project.")
	err = projectapi.DeleteProject(rtp.client, createdProject.Namespace, createdProject.Name, false)
	require.Error(rtp.T(), err)
	err = projectapi.WaitForProjectFinalizerToUpdate(rtp.client, createdProject.Name, createdProject.Namespace, 1)
	require.NoError(rtp.T(), err)
	leaderPodName, err := rancherleader.GetRancherLeaderPodName(rtp.client)
	require.NoError(rtp.T(), err)

	logCaptureStartTime = time.Now()
	log.Info("Verify that there are no errors in the Rancher logs related to role binding.")
	errorRegex := `\[ERROR\] error syncing '(.*?)': handler mgmt-auth-crtb-controller: .*? (?:not found|is forbidden), requeuing`
	err = pod.CheckPodLogsForErrors(rtp.client, clusterapi.LocalCluster, leaderPodName, rbac.RancherDeploymentNamespace, errorRegex, logCaptureStartTime)
	require.NoError(rtp.T(), err)

	logCaptureStartTime = time.Now()
	log.Info("Remove the finalizer that was previously added to the project.")
	finalizer = nil
	_, err = projects.UpdateProjectNamespaceFinalizer(rtp.client, updatedProject, finalizer)
	require.NoError(rtp.T(), err, "Failed to remove the finalizer.")

	log.Info("Verify that there are no errors in the Rancher logs related to role binding.")
	err = pod.CheckPodLogsForErrors(rtp.client, clusterapi.LocalCluster, leaderPodName, rbac.RancherDeploymentNamespace, errorRegex, logCaptureStartTime)
	require.NoError(rtp.T(), err)
}

func (rtp *RbacTerminatingProjectTestSuite) TestUserAdditionToProjectWithTerminatingProjectNamespace() {
	subSession := rtp.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a standard user.")
	createdUser, err := users.CreateUserWithRole(rtp.client, users.UserConfig(), rbac.StandardUser.String())
	require.NoError(rtp.T(), err)
	rtp.T().Logf("Created user: %v", createdUser.Username)

	log.Info("Create a project in the downstream cluster.")
	createdProject, _, err := projects.CreateProjectAndNamespaceUsingWrangler(rtp.client, rtp.cluster.ID)
	require.NoError(rtp.T(), err)

	log.Info("Simulate a project stuck in terminating state by adding a finalizer to the project.")
	finalizer := append([]string{projectapi.DummyFinalizer}, createdProject.Finalizers...)
	updatedProject, err := projects.UpdateProjectNamespaceFinalizer(rtp.client, createdProject, finalizer)
	require.NoError(rtp.T(), err, "Failed to update finalizer.")
	err = projectapi.WaitForProjectFinalizerToUpdate(rtp.client, createdProject.Name, createdProject.Namespace, 3)
	require.NoError(rtp.T(), err)

	log.Info("Delete the Project.")
	err = projectapi.DeleteProject(rtp.client, createdProject.Namespace, createdProject.Name, false)
	require.Error(rtp.T(), err)
	err = projectapi.WaitForProjectFinalizerToUpdate(rtp.client, createdProject.Name, createdProject.Namespace, 1)
	require.NoError(rtp.T(), err)

	log.Info("Add the standard user to the project as project owner.")
	_, err = rbacapi.CreateProjectRoleTemplateBinding(rtp.client, createdUser, createdProject, rbac.ProjectOwner.String())
	require.Error(rtp.T(), err)

	log.Info("Remove the finalizer that was previously added to the project.")
	finalizer = nil
	_, err = projects.UpdateProjectNamespaceFinalizer(rtp.client, updatedProject, finalizer)
	require.NoError(rtp.T(), err, "Failed to remove the finalizer.")
}

func TestRbacTerminatingProjectTestSuite(t *testing.T) {
	suite.Run(t, new(RbacTerminatingProjectTestSuite))
}

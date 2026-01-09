//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package clusterandprojectroles

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	kubeconfigapi "github.com/rancher/tests/actions/kubeconfigs"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type MembershipRolesTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (mr *MembershipRolesTestSuite) TearDownSuite() {
	mr.session.Cleanup()
}

func (mr *MembershipRolesTestSuite) SetupSuite() {
	mr.session = session.NewSession()

	client, err := rancher.NewClient("", mr.session)
	require.NoError(mr.T(), err)
	mr.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(mr.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := clusters.GetClusterIDByName(mr.client, clusterName)
	require.NoError(mr.T(), err, "Error getting cluster ID")
	mr.cluster, err = mr.client.Management.Cluster.ByID(clusterID)
	require.NoError(mr.T(), err)
}

func (mr *MembershipRolesTestSuite) TestClusterRolesOnClusterCreationAndDeletion() {
	log.Info("Creating a downstream cluster to verify membership roles on cluster creation and deletion")
	clusterObj, clusterConfig2, err := kubeconfigapi.CreateDownstreamCluster(mr.client, false)
	require.NoError(mr.T(), err)
	require.NotNil(mr.T(), clusterObj)
	require.NotNil(mr.T(), clusterConfig2)
	createdClusterID, err := clusters.GetClusterIDByName(mr.client, clusterObj.Name)
	require.NoError(mr.T(), err)

	provisioning.VerifyClusterReady(mr.T(), mr.client, clusterObj)
	err = deployment.VerifyClusterDeployments(mr.client, clusterObj)
	require.NoError(mr.T(), err)
	err = pods.VerifyClusterPods(mr.client, clusterObj)
	require.NoError(mr.T(), err)

	ownerRoleName := createdClusterID + "-" + rbacapi.ClusterOwnerRoleSuffix
	memberRoleName := createdClusterID + "-" + rbacapi.ClusterMemberRoleSuffix

	log.Infof("Verifying membership roles %s and %s exist on cluster creation", ownerRoleName, memberRoleName)
	err = rbacapi.WaitForClusterRoleExistence(mr.client, clusterapi.LocalCluster, ownerRoleName, true)
	require.NoErrorf(mr.T(), err, "Cluster role %s should exist", ownerRoleName)

	err = rbacapi.WaitForClusterRoleExistence(mr.client, clusterapi.LocalCluster, memberRoleName, true)
	require.NoErrorf(mr.T(), err, "Cluster role %s should exist", memberRoleName)

	log.Infof("Deleting the created downstream cluster %s", createdClusterID)
	err = mr.client.WranglerContext.Mgmt.Cluster().Delete(createdClusterID, &metav1.DeleteOptions{})
	require.NoError(mr.T(), err)

	log.Infof("Verifying membership roles %s and %s do not exist on cluster deletion", ownerRoleName, memberRoleName)
	err = rbacapi.WaitForClusterRoleExistence(mr.client, clusterapi.LocalCluster, ownerRoleName, false)
	require.NoErrorf(mr.T(), err, "Cluster role %s should not exist", ownerRoleName)

	err = rbacapi.WaitForClusterRoleExistence(mr.client, clusterapi.LocalCluster, memberRoleName, false)
	require.NoErrorf(mr.T(), err, "Cluster role %s should not exist", memberRoleName)
}

func (mr *MembershipRolesTestSuite) TestRolesOnProjectCreationAndDeletion() {
	log.Info("Creating a project in the downstream cluster to verify membership roles on project creation and deletion")
	createdProject, err := projectapi.CreateProject(mr.client, mr.cluster.ID)
	require.NoError(mr.T(), err)
	createdProjectID := createdProject.Name
	createdProjectNamespace := createdProject.Namespace

	ownerRoleName := createdProjectID + "-" + rbacapi.ProjectOwnerRoleSuffix
	memberRoleName := createdProjectID + "-" + rbacapi.ProjectMemberRoleSuffix

	log.Infof("Verifying membership roles %s and %s exist on project creation", ownerRoleName, memberRoleName)
	err = rbacapi.WaitForRoleExistence(mr.client, clusterapi.LocalCluster, createdProjectNamespace, ownerRoleName, true)
	require.NoErrorf(mr.T(), err, "Role %s should exist", ownerRoleName)

	err = rbacapi.WaitForRoleExistence(mr.client, clusterapi.LocalCluster, createdProjectNamespace, memberRoleName, true)
	require.NoErrorf(mr.T(), err, "Role %s should exist", memberRoleName)

	log.Infof("Deleting the created project %s in downstream cluster", createdProjectID)
	err = mr.client.WranglerContext.Mgmt.Project().Delete(createdProjectNamespace, createdProjectID, &metav1.DeleteOptions{})
	require.NoError(mr.T(), err)

	log.Infof("Verifying membership roles %s and %s do not exist on project deletion", ownerRoleName, memberRoleName)
	err = rbacapi.WaitForRoleExistence(mr.client, clusterapi.LocalCluster, createdProjectNamespace, ownerRoleName, false)
	require.NoErrorf(mr.T(), err, "Role %s should not exist", ownerRoleName)

	err = rbacapi.WaitForRoleExistence(mr.client, clusterapi.LocalCluster, createdProjectNamespace, memberRoleName, false)
	require.NoErrorf(mr.T(), err, "Role %s should not exist", memberRoleName)
}

func TestMembershipRolesTestSuite(t *testing.T) {
	suite.Run(t, new(MembershipRolesTestSuite))
}

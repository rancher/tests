//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package projects

import (
	"testing"

	v3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extnamespaceapi "github.com/rancher/shepherd/extensions/kubeapi/namespaces"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	namespaceapi "github.com/rancher/tests/actions/kubeapi/namespaces"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	userapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

type NamespaceMoveTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (s *NamespaceMoveTestSuite) SetupSuite() {
	s.session = session.NewSession()
	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)
	s.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(s.T(), clusterName, "Downstream cluster name should be set in the config file")
	clusterID, err := clusters.GetClusterIDByName(s.client, clusterName)
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), extclusterapi.LocalCluster, clusterID, "configure a downstream cluster; the suite also runs a local project-member smoke test")
	s.cluster, err = s.client.Management.Cluster.ByID(clusterID)
	require.NoError(s.T(), err)
}

func (s *NamespaceMoveTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *NamespaceMoveTestSuite) setupNamespaceMove(client *rancher.Client, clusterID, targetRole string, emptyTarget bool, namespaceCount int) ([]*v3.Project, []*rancher.Client, []*corev1.Namespace, []*corev1.Namespace) {
	role := targetRole
	if role == "custom-namespace-editor" {
		member, err := rbacapi.GetRoleTemplateByName(client, rbac.ProjectMember.String())
		require.NoError(s.T(), err)

		rules := []rbacv1.PolicyRule{
			{APIGroups: []string{""}, Resources: []string{rbacapi.NamespaceResource}, Verbs: []string{rbacapi.GetVerb, rbacapi.UpdateVerb, rbacapi.PatchVerb}},
		}
		custom, err := rbacapi.CreateRoleTemplate(client, rbacapi.ProjectContext, rules, []*v3.RoleTemplate{member}, false, false, nil)
		require.NoError(s.T(), err)
		role = custom.Name
	}

	sourceProject, err := projectapi.CreateProject(client, clusterID)
	require.NoError(s.T(), err)
	sourceUser, sourceUserClient, err := userapi.CreateUserWithRolesAndClient(client, rbac.StandardUser.String())
	require.NoError(s.T(), err)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(client, sourceUser.Name, sourceProject, rbac.ProjectMember.String())
	require.NoError(s.T(), err)

	targetProject, err := projectapi.CreateProject(client, clusterID)
	require.NoError(s.T(), err)
	targetUser, targetUserClient, err := userapi.CreateUserWithRolesAndClient(client, rbac.StandardUser.String())
	require.NoError(s.T(), err)
	_, err = rbacapi.CreateProjectRoleTemplateBinding(client, targetUser.Name, targetProject, role)
	require.NoError(s.T(), err)

	otherProject, err := projectapi.CreateProject(client, clusterID)
	require.NoError(s.T(), err)

	sourceUnmovedNamespace, err := namespaceapi.CreateNamespace(client, clusterID, sourceProject.Name, namegen.AppendRandomString("move-rbac-"), "", nil, nil)
	require.NoError(s.T(), err)

	var targetUnmovedNamespace *corev1.Namespace
	if !emptyTarget {
		targetUnmovedNamespace, err = namespaceapi.CreateNamespace(client, clusterID, targetProject.Name, namegen.AppendRandomString("move-rbac-"), "", nil, nil)
		require.NoError(s.T(), err)
	}
	unrelated, err := namespaceapi.CreateNamespace(client, clusterID, otherProject.Name, namegen.AppendRandomString("move-rbac-"), "", nil, nil)
	require.NoError(s.T(), err)

	var movingNamespaces []*corev1.Namespace
	for range namespaceCount {
		ns, err := namespaceapi.CreateNamespace(client, clusterID, sourceProject.Name, namegen.AppendRandomString("move-rbac-"), "", nil, nil)
		require.NoError(s.T(), err)
		movingNamespaces = append(movingNamespaces, ns)
	}

	return []*v3.Project{sourceProject, targetProject}, []*rancher.Client{sourceUserClient, targetUserClient},
		[]*corev1.Namespace{sourceUnmovedNamespace, targetUnmovedNamespace, unrelated}, movingNamespaces
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceProjectMember() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving a namespace grants the destination project member read and edit access and revokes source user access")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, rbac.ProjectMember.String(), false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceProjectMemberLocal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving a local cluster namespace grants the destination project member read and edit access and revokes source user access")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, extclusterapi.LocalCluster, rbac.ProjectMember.String(), false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, extclusterapi.LocalCluster, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, extclusterapi.LocalCluster, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, extclusterapi.LocalCluster, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, extclusterapi.LocalCluster, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, extclusterapi.LocalCluster, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceProjectOwner() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving a namespace grants the destination project owner read and edit access and revokes source user access")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, rbac.ProjectOwner.String(), false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceReadOnly() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving a namespace grants the destination read-only user read access, denies writes, and revokes source user access")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, "read-only", false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.CreateVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.True(s.T(), apierrors.IsForbidden(err), "read-only namespace UPDATE must be forbidden, got %v", err)

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.NotEqual(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceCustomEditor() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving a namespace grants the destination user with a custom role read and edit access and revokes source user access")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, "custom-namespace-editor", false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceRoundTrip() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that repeated namespace moves grant access to users of the current project and revoke access from users of the previous project")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, rbac.ProjectMember.String(), false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err = namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, projects[0].Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err = namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveSeveralNamespaces() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving two namespaces grants destination user access and revokes source user access to both namespaces")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, rbac.ProjectMember.String(), false, 2)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]
	secondNamespaceToMove := movingNamespaces[1]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", secondNamespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, secondNamespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", secondNamespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, secondNamespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)
	err = namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, secondNamespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", secondNamespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, secondNamespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", secondNamespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, secondNamespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])

	secondUpdatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, secondNamespaceToMove.Name)
	require.NoError(s.T(), err)

	secondUpdatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, secondUpdatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	secondPersistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, secondNamespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, secondPersistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceToEmptyProject() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that moving a namespace into an empty project grants destination user access and revokes source user access")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, rbac.ProjectMember.String(), true, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func (s *NamespaceMoveTestSuite) TestMoveNamespaceOutOfAllProjects() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that removing a namespace from its project denies both project users access and reassignment grants access only to the destination user")

	projects, userClients, stableNamespaces, movingNamespaces := s.setupNamespaceMove(s.client, s.cluster.ID, rbac.ProjectMember.String(), false, 1)
	targetProject := projects[1]
	sourceUserClient, targetUserClient := userClients[0], userClients[1]
	sourceUnmovedNamespace, unrelatedNamespace := stableNamespaces[0], stableNamespaces[2]
	targetUnmovedNamespace := stableNamespaces[1]
	namespaceToMove := movingNamespaces[0]

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err := namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, "")
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	err = namespaceapi.MoveNamespaceToProject(s.client, s.cluster.ID, namespaceToMove.Name, targetProject.Name)
	require.NoError(s.T(), err)

	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", namespaceToMove.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, namespaceToMove.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", sourceUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, sourceUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", true, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", targetUnmovedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, targetUnmovedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(sourceUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.GetVerb, rbacapi.NamespaceResource, "", unrelatedNamespace.Name, false, false))
	require.NoError(s.T(), rbacapi.VerifyUserPermission(targetUserClient, s.cluster.ID, rbacapi.ListVerb, rbacapi.ConfigMapResource, unrelatedNamespace.Name, "", false, false))

	updatedNamespace, err := extnamespaceapi.GetNamespaceByName(targetUserClient, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)

	updatedNamespace.Labels["move-rbac-verified"] = namespaceapi.TrueLabelValue
	_, err = extnamespaceapi.UpdateNamespace(targetUserClient, s.cluster.ID, updatedNamespace)
	require.NoError(s.T(), err, "destination namespace editor must be able to update the namespace")

	persistedNamespace, err := extnamespaceapi.GetNamespaceByName(s.client, s.cluster.ID, namespaceToMove.Name)
	require.NoError(s.T(), err)
	require.Equal(s.T(), namespaceapi.TrueLabelValue, persistedNamespace.Labels["move-rbac-verified"])
}

func TestNamespaceMoveTestSuite(t *testing.T) {
	suite.Run(t, new(NamespaceMoveTestSuite))
}

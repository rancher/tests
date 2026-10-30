//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package multiidp

import (
	"testing"

	managementv3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/sirupsen/logrus"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type MultiIdpRBACSuite struct {
	suite.Suite
	session   *session.Session
	client    *rancher.Client
	cluster   *v3.Cluster
	instances []authactions.NamedOpenLdapInstance
}

func (r *MultiIdpRBACSuite) SetupSuite() {
	r.session = session.NewSession()

	client, err := rancher.NewClient("", r.session)
	require.NoError(r.T(), err, "Failed to create Rancher client")
	r.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(r.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(r.client, clusterName)
	require.NoError(r.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	r.cluster, err = r.client.Management.Cluster.ByID(clusterID)
	require.NoError(r.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	r.instances, err = LoadInstances()
	require.NoError(r.T(), err, "Failed to load the multi IdP instances")
}

func (r *MultiIdpRBACSuite) TearDownSuite() {
	r.session.Cleanup()

	for _, err := range TearDownInstances(r.client, r.instances) {
		require.NoError(r.T(), err, "Failed to remove an auth config in teardown")
	}
}

func (r *MultiIdpRBACSuite) bindGroup(authAdmin *rancher.Client, instance *authactions.NamedOpenLdapInstance) *managementv3.ClusterRoleTemplateBinding {
	require.NotEmpty(r.T(), instance.Directory.Group, "%s needs a group to bind", instance.ConfigName)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, r.cluster.ID, instance.GroupPrincipalID(instance.Directory.Group), rbac.ClusterMember.String())
	require.NoError(r.T(), err, "Failed to bind the group of %s", instance.ConfigName)

	return crtb
}

func (r *MultiIdpRBACSuite) deleteBinding(authAdmin *rancher.Client, crtb *managementv3.ClusterRoleTemplateBinding) {
	require.NoError(r.T(), DeleteClusterRoleTemplateBinding(authAdmin, crtb), "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
}

func (r *MultiIdpRBACSuite) TestGroupClusterAccessPerConfig() {
	bound, other := &r.instances[0], &r.instances[1]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(r.client, r.session, bound)
	require.NoError(r.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	require.NoError(r.T(), authactions.EnsureNamedOpenLdapEnabled(r.client, other), "Failed to enable %s", other.ConfigName)

	crtb := r.bindGroup(authAdmin, bound)
	defer r.deleteBinding(authAdmin, crtb)

	require.NotEmpty(r.T(), bound.Directory.Users, "%s needs users in the bound group", bound.ConfigName)
	for _, userInfo := range bound.Directory.Users {
		userClient, err := LoginNamedUser(authAdmin, bound, userInfo)
		require.NoError(r.T(), err, "Failed to login user [%v]", userInfo.Username)

		rbac.VerifyUserCanListCluster(r.T(), r.client, userClient, r.cluster.ID, rbac.ClusterMember)
	}

	for _, userInfo := range other.Directory.Users {
		userClient, err := LoginNamedUser(authAdmin, other, userInfo)
		require.NoError(r.T(), err, "Failed to login user [%v]", userInfo.Username)

		require.NoError(r.T(), VerifyCannotListClusters(userClient), "User [%v] of %s must not list clusters through a binding on %s", userInfo.Username, other.ConfigName, bound.ConfigName)
	}
}

func (r *MultiIdpRBACSuite) TestGroupClusterAccessDynamic() {
	for i := range r.instances {
		r.verifyGroupClusterAccess(&r.instances[i])
	}
}

// Cleanup is deferred so a failed assertion cannot leave the binding behind to collide with the next test.
func (r *MultiIdpRBACSuite) verifyGroupClusterAccess(instance *authactions.NamedOpenLdapInstance) {
	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(r.client, r.session, instance)
	require.NoError(r.T(), err, "Failed to setup authenticated test for %s", instance.ConfigName)
	defer subSession.Cleanup()

	crtb := r.bindGroup(authAdmin, instance)
	defer r.deleteBinding(authAdmin, crtb)

	for _, userInfo := range instance.Directory.Users {
		userClient, err := LoginNamedUser(authAdmin, instance, userInfo)
		require.NoError(r.T(), err, "Failed to login user [%v]", userInfo.Username)

		r.verifyGroupStored(userClient, instance)
		rbac.VerifyUserCanListCluster(r.T(), r.client, userClient, r.cluster.ID, rbac.ClusterMember)
	}
}

func (r *MultiIdpRBACSuite) verifyGroupStored(userClient *rancher.Client, instance *authactions.NamedOpenLdapInstance) {
	groupID := instance.GroupPrincipalID(instance.Directory.Group)

	stored, err := authactions.UserAttributeGroupPrincipalIDs(r.client, userClient.UserID, instance.ConfigName)
	require.NoError(r.T(), err, "Failed to read the groups stored for the user")
	require.Contains(r.T(), stored, groupID, "Login should store group %s under %s, so check the user is in that group", groupID, instance.ConfigName)
}

func (r *MultiIdpRBACSuite) TestRancherRequestGroupsPerConfig() {
	instance := &r.instances[0]

	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(r.client, r.session, instance)
	require.NoError(r.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	crtb := r.bindGroup(authAdmin, instance)
	defer r.deleteBinding(authAdmin, crtb)

	require.NotEmpty(r.T(), instance.Directory.Users, "%s needs users in the group", instance.ConfigName)
	r.verifyRequestGroups(authAdmin, instance, instance.Directory.Users[0])
}

func (r *MultiIdpRBACSuite) TestRancherRequestGroupsDynamic() {
	for i := range r.instances {
		r.verifyInstanceRequestGroups(&r.instances[i])
	}
}

func (r *MultiIdpRBACSuite) verifyInstanceRequestGroups(instance *authactions.NamedOpenLdapInstance) {
	subSession, authAdmin, err := authactions.SetupNamedOpenLdapSession(r.client, r.session, instance)
	require.NoError(r.T(), err, "Failed to setup authenticated test for %s", instance.ConfigName)
	defer subSession.Cleanup()

	crtb := r.bindGroup(authAdmin, instance)
	defer r.deleteBinding(authAdmin, crtb)

	for _, userInfo := range instance.Directory.Users {
		r.verifyRequestGroups(authAdmin, instance, userInfo)
	}
}

func (r *MultiIdpRBACSuite) verifyRequestGroups(authAdmin *rancher.Client, instance *authactions.NamedOpenLdapInstance, userInfo authactions.User) {
	require.NotEmpty(r.T(), instance.Directory.Group, "%s needs a group the users belong to", instance.ConfigName)

	userClient, err := LoginNamedUser(authAdmin, instance, userInfo)
	require.NoError(r.T(), err, "Failed to login user [%v]", userInfo.Username)

	r.verifyGroupStored(userClient, instance)

	groups, err := authactions.RancherRequestGroups(userClient, r.cluster.ID)
	require.NoError(r.T(), err, "Failed to read the groups of user [%v]", userInfo.Username)
	require.Contains(r.T(), groups, instance.GroupPrincipalID(instance.Directory.Group), "User [%v] should carry the group stored under %s", userInfo.Username, instance.ConfigName)
}

func TestMultiIdpRBACSuite(t *testing.T) {
	suite.Run(t, new(MultiIdpRBACSuite))
}

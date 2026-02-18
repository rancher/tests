//go:build (validation || infra.any || cluster.any || extended) && !stress

package rbac

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/hostedtenant"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type HostedRancherTestSuite struct {
	suite.Suite
	session      *session.Session
	hostClient   *rancher.Client
	tenantClient *rancher.Client
}

func (h *HostedRancherTestSuite) TearDownSuite() {
	h.session.Cleanup()
}

func (h *HostedRancherTestSuite) SetupSuite() {
	h.session = session.NewSession()

	client, err := rancher.NewClient("", h.session)
	require.NoError(h.T(), err)
	h.hostClient = client

	var tenantConfig hostedtenant.Config
	config.LoadConfig(hostedtenant.ConfigurationFileKey, &tenantConfig)
	require.NotEmpty(h.T(), tenantConfig.Clients, "No tenant clients configured")

	tenantClientConfig := &rancher.Config{
		Host:        tenantConfig.Clients[0].Host,
		AdminToken:  tenantConfig.Clients[0].AdminToken,
		Cleanup:     tenantConfig.Clients[0].Cleanup,
		Insecure:    tenantConfig.Clients[0].Insecure,
		ClusterName: tenantConfig.Clients[0].ClusterName,
	}

	tenantClient, err := rancher.NewClientForConfig(tenantConfig.Clients[0].AdminToken, tenantClientConfig, h.session)
	require.NoError(h.T(), err)
	h.tenantClient = tenantClient
}

func (h *HostedRancherTestSuite) TestGlobalRoleInheritedClusterRoles() {
	subSession := h.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create global role with inheritedClusterRoles")
	inheritedClusterRoles := []string{rbac.ClusterOwner.String()}
	createdGlobalRole, err := rbacapi.CreateGlobalRoleWithInheritedClusterRoles(h.hostClient, inheritedClusterRoles)
	require.NoError(h.T(), err)

	log.Info("Verify the global role is created on the hosted Rancher")
	_, err = h.hostClient.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.NoError(h.T(), err, "Expected global role to exist on host")

	log.Info("Verify the global role is not created in the tenant Rancher server")
	_, err = h.tenantClient.WranglerContext.Mgmt.GlobalRole().Get(createdGlobalRole.Name, metav1.GetOptions{})
	require.Error(h.T(), err, "Expected global role to not exist on tenant")

	log.Info("Create a user with global roles")
	createdUser, err := users.CreateUserWithRole(h.hostClient, users.UserConfig(), rbac.StandardUser.String(), createdGlobalRole.Name)
	require.NoError(h.T(), err)

	log.Info("Verify global role bindings on host")
	hostGrb, err := rbacapi.GetGlobalRoleBindingByUserAndRole(h.hostClient, createdUser.ID, createdGlobalRole.Name)
	require.NoError(h.T(), err)
	require.Equal(h.T(), hostGrb.GlobalRoleName, createdGlobalRole.Name)

	log.Info("Verify no global role bindings on tenant")
	_, err = rbacapi.GetGlobalRoleBindingByUserAndRole(h.tenantClient, createdUser.ID, createdGlobalRole.Name)
	require.Error(h.T(), err, "Expected error because global role binding should not exist on tenant")
	require.Contains(h.T(), err.Error(), "context deadline exceeded", "Expected timeout error when GRB is not found")

	grbName := hostGrb.Name

	log.Info("Verify CRTB on host")
	crtbs, err := rbacapi.ListCRTBsByLabel(h.hostClient, rbac.GrbOwnerLabel, grbName, len(inheritedClusterRoles))
	require.NoError(h.T(), err)
	require.GreaterOrEqual(h.T(), len(crtbs.Items), len(inheritedClusterRoles), "Should have CRTBs on host")

	log.Info("Verify role bindings on host")
	for _, crtb := range crtbs.Items {
		namespaces := []string{rbac.GlobalDataNS, crtb.ClusterName}
		userRBs, err := rbacapi.GetRoleBindingsForUsers(h.hostClient, crtb.UserName, namespaces)
		require.NoError(h.T(), err)
		require.Greater(h.T(), len(userRBs), 0, "Should have role bindings on host for user %s", crtb.UserName)
	}

	log.Info("Verify cluster role bindings on host")
	userCRBs, err := rbacapi.GetClusterRoleBindingsForUsers(h.hostClient, crtbs)
	require.NoError(h.T(), err)
	require.Greater(h.T(), len(userCRBs), 0, "Should have cluster role bindings on host")

	log.Info("Verify tenant (as a downstream cluster) has proper access")
	tenantClusterIDAsDownstreamCluster, err := clusters.GetClusterIDByName(h.hostClient, h.hostClient.RancherConfig.ClusterName)
	require.NoError(h.T(), err)
	userClient, err := h.hostClient.AsUser(createdUser)
	require.NoError(h.T(), err)
	cluster, err := userClient.Management.Cluster.ByID(tenantClusterIDAsDownstreamCluster)
	require.NoError(h.T(), err)
	require.NotNil(h.T(), cluster, "User should be able to access tenant cluster")
}

func TestHostedRancherTestSuite(t *testing.T) {
	suite.Run(t, new(HostedRancherTestSuite))
}

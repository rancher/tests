//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package kubeapiauth

import (
	"context"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type KubeAPIAuthProviderSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	clusterID  string
	cluster    *management.Cluster
	restoreACE func() error
}

func (k *KubeAPIAuthProviderSuite) SetupSuite() {
	k.session = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err, "Failed to create Rancher client")
	k.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(k.T(), clusterName, "Cluster name should be set")

	k.clusterID, err = clusters.GetClusterIDByName(k.client, clusterName)
	require.NoError(k.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	k.cluster, err = k.client.Management.Cluster.ByID(k.clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", k.clusterID)

	require.Equal(k.T(), activeClusterState, k.cluster.State,
		"Cluster [%v] is %v rather than %v; an authorized cluster endpoint login needs the cluster up", clusterName, k.cluster.State, activeClusterState)

	logrus.Infof("Making sure the authorized cluster endpoint is on, which is what deploys the %v daemon set", authactions.KubeAPIAuthDaemonSet)
	k.restoreACE, err = authactions.EnsureACEEnabled(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to enable the authorized cluster endpoint on cluster [%v]", clusterName)
}

func (k *KubeAPIAuthProviderSuite) TearDownSuite() {
	defer k.session.Cleanup()

	if k.restoreACE != nil {
		logrus.Info("Putting the authorized cluster endpoint back the way the suite found it")
		require.NoError(k.T(), k.restoreACE(), "Failed to restore the authorized cluster endpoint")
	}
}

func (k *KubeAPIAuthProviderSuite) reachClusterAsGroupMember(providerName string, admin *management.User, group, groupPrincipalID string, member authactions.User) {
	wasEnabled, err := authactions.AuthProviderEnabled(k.client, providerName)
	require.NoError(k.T(), err, "Failed to read whether %v was already on", providerName)

	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(k.client, k.session, admin, providerName)
	require.NoError(k.T(), err, "Failed to sign in as the %v administrator", providerName)
	defer subSession.Cleanup()

	if !wasEnabled {
		subSession.RegisterCleanupFunc(func() error {
			return authactions.EnsureAuthProviderDisabled(k.client, providerName)
		})
	}

	logrus.Infof("Granting the %v group [%v] the %v role on cluster [%v]", providerName, group, rbac.ClusterOwner, k.clusterID)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, k.clusterID, groupPrincipalID, rbac.ClusterOwner.String())
	require.NoError(k.T(), err, "Failed to bind the %v group to the cluster", providerName)

	subSession.RegisterCleanupFunc(func() error {
		return authAdmin.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
	})

	userClient, err := authactions.LoginAsAuthUser(authAdmin, &management.User{Username: member.Username, Password: member.Password}, providerName)
	require.NoError(k.T(), err, "Failed to sign in as %v user [%v]", providerName, member.Username)

	clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts of %v user [%v]", providerName, member.Username)

	for name, clientset := range clientsets {
		logrus.Infof("Listing namespaces as %v user [%v] through context [%v]", providerName, member.Username, name)

		_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.NoError(k.T(), err,
			"A %v user whose group holds the %v role should reach the cluster through context [%v], so the authorized cluster endpoint honours a binding made against an external group", providerName, rbac.ClusterOwner, name)

		review, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(context.Background(), &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
		require.NoError(k.T(), err, "The cluster should resolve the identity behind context [%v]", name)
		require.Equal(k.T(), userClient.UserID, review.Status.UserInfo.Username,
			"kube-api-auth should resolve the token to the Rancher user the %v login created, which is what the group binding applies to", providerName)
	}
}

func (k *KubeAPIAuthProviderSuite) TestACEAuthenticatesAnOpenLDAPGroupMember() {
	authConfig := new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, authConfig)
	require.NotEmpty(k.T(), authConfig.DoubleNestedGroup, "The OpenLDAP configuration should name the group the cluster role is bound to")
	require.NotEmpty(k.T(), authConfig.DoubleNestedUsers, "The OpenLDAP configuration should name the users that belong to the group")

	admin := &management.User{
		Username: k.client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: k.client.Auth.OLDAP.Config.Users.Admin.Password,
	}
	principalID := authactions.GetGroupPrincipalID(authactions.OpenLdap, authConfig.DoubleNestedGroup,
		k.client.Auth.OLDAP.Config.Users.SearchBase, k.client.Auth.OLDAP.Config.Groups.SearchBase)

	k.reachClusterAsGroupMember(authactions.OpenLdap, admin, authConfig.DoubleNestedGroup, principalID, authConfig.DoubleNestedUsers[0])
}

func (k *KubeAPIAuthProviderSuite) TestACEAuthenticatesAnActiveDirectoryGroupMember() {
	authConfig := new(authactions.AuthConfig)
	config.LoadConfig(authactions.ActiveDirectoryAuthInput, authConfig)
	require.NotEmpty(k.T(), authConfig.DoubleNestedGroup, "The Active Directory configuration should name the group the cluster role is bound to")
	require.NotEmpty(k.T(), authConfig.DoubleNestedUsers, "The Active Directory configuration should name the users that belong to the group")

	admin := &management.User{
		Username: k.client.Auth.ActiveDirectory.Config.Users.Admin.Username,
		Password: k.client.Auth.ActiveDirectory.Config.Users.Admin.Password,
	}
	principalID := authactions.GetGroupPrincipalID(authactions.ActiveDirectory, authConfig.DoubleNestedGroup,
		k.client.Auth.ActiveDirectory.Config.Users.SearchBase, k.client.Auth.ActiveDirectory.Config.Groups.SearchBase)

	k.reachClusterAsGroupMember(authactions.ActiveDirectory, admin, authConfig.DoubleNestedGroup, principalID, authConfig.DoubleNestedUsers[0])
}

func (k *KubeAPIAuthProviderSuite) TestACEAuthenticatesAKeycloakSAMLGroupMember() {
	keycloakClient, err := authactions.NewKeycloakClient(k.session)
	require.NoError(k.T(), err, "Failed to create the Keycloak admin client")

	logrus.Info("Building the Keycloak realm the SAML provider authenticates against")
	keycloakSAMLFixture, err := authactions.SetupKeycloakSAML(k.client, keycloakClient)
	require.NoError(k.T(), err, "Failed to set up the Keycloak realm for Keycloak SAML")
	require.NotEmpty(k.T(), keycloakSAMLFixture.AuthInput.Group, "Keycloak SAML setup should have settled on the group the cluster role is bound to")
	require.NotEmpty(k.T(), keycloakSAMLFixture.AuthInput.Users, "Keycloak SAML setup should have settled on the users that belong to the group")

	admin := &management.User{Username: keycloakSAMLFixture.Admin.Username, Password: keycloakSAMLFixture.Admin.Password}
	principalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakSAML, keycloakSAMLFixture.AuthInput.Group)

	k.reachClusterAsGroupMember(authactions.KeycloakSAML, admin, keycloakSAMLFixture.AuthInput.Group, principalID, keycloakSAMLFixture.AuthInput.Users[0])
}

func (k *KubeAPIAuthProviderSuite) TestACEAuthenticatesAKeycloakOIDCGroupMember() {
	keycloakClient, err := authactions.NewKeycloakOIDCClient(k.session)
	require.NoError(k.T(), err, "Failed to create the Keycloak admin client")

	logrus.Info("Building the Keycloak realm the OpenID Connect provider authenticates against")
	keycloakOIDCFixture, err := authactions.SetupKeycloakOIDC(k.client, keycloakClient)
	require.NoError(k.T(), err, "Failed to set up the Keycloak realm for Keycloak OIDC")
	require.NotEmpty(k.T(), keycloakOIDCFixture.AuthInput.Group, "Keycloak OIDC setup should have settled on the group the cluster role is bound to")
	require.NotEmpty(k.T(), keycloakOIDCFixture.AuthInput.Users, "Keycloak OIDC setup should have settled on the users that belong to the group")

	admin := &management.User{Username: keycloakOIDCFixture.Admin.Username, Password: keycloakOIDCFixture.Admin.Password}
	principalID := authactions.GetExternalGroupPrincipalID(authactions.KeycloakOIDC, keycloakOIDCFixture.AuthInput.Group)

	k.reachClusterAsGroupMember(authactions.KeycloakOIDC, admin, keycloakOIDCFixture.AuthInput.Group, principalID, keycloakOIDCFixture.AuthInput.Users[0])
}

func TestKubeAPIAuthProviderSuite(t *testing.T) {
	suite.Run(t, new(KubeAPIAuthProviderSuite))
}

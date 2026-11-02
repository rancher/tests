//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package multiidp

import (
	"slices"
	"strings"
	"testing"
	"github.com/sirupsen/logrus"

	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	localProviderID        = "local"
	genericOIDCProvider    = "genericoidc"
	defaultPrincipalPrefix = "openldap_user://"
)

type MultiIdpDefaultsSuite struct {
	suite.Suite
	session   *session.Session
	client    *rancher.Client
	cluster   *v3.Cluster
	instances []authactions.NamedOpenLdapInstance
}

func (d *MultiIdpDefaultsSuite) SetupSuite() {
	d.session = session.NewSession()

	client, err := rancher.NewClient("", d.session)
	require.NoError(d.T(), err, "Failed to create Rancher client")
	d.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(d.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(d.client, clusterName)
	require.NoError(d.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	d.cluster, err = d.client.Management.Cluster.ByID(clusterID)
	require.NoError(d.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	d.instances, err = LoadInstances()
	require.NoError(d.T(), err, "Failed to load the multi IdP instances")
}

func (d *MultiIdpDefaultsSuite) TearDownSuite() {
	d.session.Cleanup()

	defaultConfig, err := d.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	if err == nil && defaultConfig.Enabled {
		require.NoError(d.T(), d.client.Auth.OLDAP.Disable(), "Failed to disable the default OpenLDAP config in teardown")
	}

	for _, err := range TearDownInstances(d.client, d.instances) {
		require.NoError(d.T(), err, "Failed to remove an auth config in teardown")
	}
}

func (d *MultiIdpDefaultsSuite) TestProviderTypesListed() {
	err := authactions.VerifyProviderTypesInclude(d.client,
		localProviderID,
		authactions.OpenLdap,
		authactions.ActiveDirectory,
		authactions.KeycloakSAML,
		authactions.GenericSAML,
		authactions.KeycloakOIDC,
		genericOIDCProvider,
	)
	require.NoError(d.T(), err, "Rancher should list every auth provider type it offers")
}

func (d *MultiIdpDefaultsSuite) TestDefaultConfigCoexistsWithNamedConfigs() {
	defaultInput := d.client.Auth.OLDAP.Config
	if defaultInput == nil || (defaultInput.Hostname == "" && defaultInput.IP == "") || defaultInput.Users == nil || defaultInput.Users.Admin == nil {
		d.T().Skip("the default openLDAP config is not set, there is no default config to compare against")
	}

	named := &d.instances[0]

	require.NoError(d.T(), authactions.EnsureAuthProviderEnabled(d.client, authactions.OpenLdap), "Failed to enable the default OpenLDAP config")
	require.NoError(d.T(), authactions.EnsureNamedOpenLdapEnabled(d.client, named), "Failed to enable %s", named.ConfigName)

	_, err := extsecretapi.GetSecretByName(d.client, extclusterapi.LocalCluster, rbac.GlobalDataNS, authactions.OpenLdapPasswordSecretID)
	require.NoError(d.T(), err, "The default config should keep its type based secret name")

	subSession := d.session.NewSession()
	defer subSession.Cleanup()

	authClient, err := d.client.WithSession(subSession)
	require.NoError(d.T(), err, "Failed to create client with new session")

	defaultAdmin := &v3.User{Username: defaultInput.Users.Admin.Username, Password: defaultInput.Users.Admin.Password}
	defaultClient, err := authactions.LoginAsAuthUser(authClient, defaultAdmin, authactions.OpenLdap)
	require.NoError(d.T(), err, "The default config should still sign users in without a configName")

	defaultUser, err := d.client.Management.User.ByID(defaultClient.UserID)
	require.NoError(d.T(), err, "Failed to retrieve the default config user")
	require.True(d.T(), slices.ContainsFunc(defaultUser.PrincipalIDs, func(id string) bool {
		return strings.HasPrefix(id, defaultPrincipalPrefix)
	}), "The default config should issue %s principals, got %v", defaultPrincipalPrefix, defaultUser.PrincipalIDs)

	_, err = authactions.LoginAsNamedAuthUser(authClient, named.AdminUser(), named.ConfigName)
	require.NoError(d.T(), err, "%s should keep working next to the default config", named.ConfigName)
}

func (d *MultiIdpDefaultsSuite) TestDefaultConfigGroupClusterAccess() {
	defaultInput := d.client.Auth.OLDAP.Config
	if defaultInput == nil || (defaultInput.Hostname == "" && defaultInput.IP == "") || defaultInput.Users == nil || defaultInput.Users.Admin == nil || defaultInput.Groups == nil {
		d.T().Skip("the default openLDAP config is not set, there is no default config to compare against")
	}

	defaultDirectory := new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, defaultDirectory)
	if defaultDirectory.Group == "" || len(defaultDirectory.Users) == 0 {
		d.T().Skip("the default authInput has no group and users to bind")
	}

	named := &d.instances[0]
	require.NotEmpty(d.T(), named.Directory.Users, "%s needs users to sign in as", named.ConfigName)

	require.NoError(d.T(), authactions.EnsureAuthProviderEnabled(d.client, authactions.OpenLdap), "Failed to enable the default OpenLDAP config")
	require.NoError(d.T(), authactions.EnsureNamedOpenLdapEnabled(d.client, named), "Failed to enable %s", named.ConfigName)

	subSession := d.session.NewSession()
	defer subSession.Cleanup()

	authClient, err := d.client.WithSession(subSession)
	require.NoError(d.T(), err, "Failed to create client with new session")

	groupID := authactions.GetGroupPrincipalID(authactions.OpenLdap, defaultDirectory.Group, defaultInput.Users.SearchBase, defaultInput.Groups.SearchBase)
	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(d.client, d.cluster.ID, groupID, rbac.ClusterMember.String())
	require.NoError(d.T(), err, "Failed to bind the group of the default config")

	defer func() {
		err = d.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Delete(crtb.Namespace, crtb.Name, &metav1.DeleteOptions{})
		require.NoError(d.T(), err, "Failed to delete CRTB: %s/%s", crtb.Namespace, crtb.Name)
	}()

	for _, userInfo := range defaultDirectory.Users {
		userClient, err := authactions.LoginAsAuthUser(authClient, &v3.User{Username: userInfo.Username, Password: userInfo.Password}, authactions.OpenLdap)
		require.NoError(d.T(), err, "Failed to login user [%v] through the default config", userInfo.Username)

		rbac.VerifyUserCanListCluster(d.T(), d.client, userClient, d.cluster.ID, rbac.ClusterMember)
	}

	namedUser := named.Directory.Users[0]
	namedClient, err := LoginNamedUser(authClient, named, namedUser)
	require.NoError(d.T(), err, "Failed to login user [%v] through %s", namedUser.Username, named.ConfigName)

	require.NoError(d.T(), VerifyCannotListClusters(namedClient), "User [%v] of %s must not list clusters through a binding on the default config", namedUser.Username, named.ConfigName)
}

func (d *MultiIdpDefaultsSuite) TestSecretNamesFollowConfigNamesDynamic() {
	defaultSecret := authactions.NamedConfigSecretID(authactions.OpenLdap, authactions.OpenLdap, authactions.OpenLdapConfigType, authactions.OpenLdapSecretField)

	for i := range d.instances {
		instance := &d.instances[i]
		require.NoError(d.T(), authactions.EnsureNamedOpenLdapEnabled(d.client, instance), "Failed to enable %s", instance.ConfigName)

		secretID := authactions.NamedConfigSecretID(instance.ConfigName, authactions.OpenLdap, authactions.OpenLdapConfigType, authactions.OpenLdapSecretField)
		require.NotEqual(d.T(), defaultSecret, secretID, "%s must not share the default config's secret", instance.ConfigName)

		_, err := extsecretapi.GetSecretByName(d.client, extclusterapi.LocalCluster, rbac.GlobalDataNS, secretID)
		require.NoError(d.T(), err, "%s should have the secret %s", instance.ConfigName, secretID)
	}
}
func TestMultiIdpDefaultsSuite(t *testing.T) {
	suite.Run(t, new(MultiIdpDefaultsSuite))
}

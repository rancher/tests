//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package activedirectory

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	authconfigsapi "github.com/rancher/tests/actions/kubeapi/authconfigs"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

const (
	principalIDAttribute        = "sAMAccountName"
	principalIDChangedAttribute = "cn"
)

type ActiveDirectoryPrincipalIDTestSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	clusterID  string
	adminUser  *v3.User
	authConfig *authactions.AuthConfig
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TearDownSuite() {
	a.session.Cleanup()
}

func (a *ActiveDirectoryPrincipalIDTestSuite) SetupSuite() {
	a.session = session.NewSession()

	client, err := rancher.NewClient("", a.session)
	require.NoError(a.T(), err, "Failed to create Rancher client")
	a.client = client

	supported, err := actionsClusters.IsRancherVersionAbove(a.client, "v2.16.0-0")
	require.NoError(a.T(), err, "Failed to read the Rancher server version")
	if !supported {
		a.T().Skip("Configurable LDAP principal ID attributes are only in Rancher v2.16 and later")
	}

	a.authConfig = new(authactions.AuthConfig)
	config.LoadConfig(authactions.ActiveDirectoryAuthInput, a.authConfig)
	require.NotEmpty(a.T(), a.authConfig.Users, "The Active Directory configuration should name the users of its group")

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(a.T(), clusterName, "Cluster name should be set")
	a.clusterID, err = clusters.GetClusterIDByName(a.client, clusterName)
	require.NoError(a.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	a.adminUser = &v3.User{
		Username: client.Auth.ActiveDirectory.Config.Users.Admin.Username,
		Password: client.Auth.ActiveDirectory.Config.Users.Admin.Password,
	}

	restore, err := authactions.CaptureActiveDirectoryRestore(a.client)
	require.NoError(a.T(), err, "Failed to record the Active Directory settings to restore")
	a.session.RegisterCleanupFunc(restore)

	a.T().Log("Disabling Active Directory so it can be enabled with principal ID attributes")
	require.NoError(a.T(), authactions.DisableAuthProviderAndWait(a.client, authactions.ActiveDirectory), "Failed to disable Active Directory")

	a.T().Logf("Enabling Active Directory with userIDAttribute and groupIDAttribute %s", principalIDAttribute)
	require.NoError(a.T(), authactions.EnableActiveDirectoryWithIDAttributes(a.client, principalIDAttribute, principalIDAttribute), "Failed to enable Active Directory with principal ID attributes")
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TestActiveDirectoryLoginUsesUserIDAttribute() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	adUser := a.authConfig.Users[0]
	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: adUser.Username, Password: adUser.Password}, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Active Directory user %s should be able to log in", adUser.Username)

	user, err := extuserapi.GetUserByName(authAdmin, userClient.UserID)
	require.NoError(a.T(), err)

	expectedPrincipalID := authactions.FormatPrincipalID(authactions.ActiveDirectory, authactions.PrincipalTypeUser, adUser.Username)
	require.Contains(a.T(), user.PrincipalIDs, expectedPrincipalID, "The login should identify the user by its %s attribute", principalIDAttribute)

	for _, principalID := range user.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.FormatPrincipalID(authactions.ActiveDirectory, authactions.PrincipalTypeUser, "")) {
			require.Equal(a.T(), expectedPrincipalID, principalID, "The user should carry no DN-based Active Directory principal")
		}
	}
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TestActiveDirectoryPrincipalSearchReturnsIDAttributeValues() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userName := a.authConfig.Users[0].Username
	expectedUserPrincipalID := authactions.FormatPrincipalID(authactions.ActiveDirectory, authactions.PrincipalTypeUser, userName)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, userName, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(a.T(), err, "A user search should return the ID built from the %s attribute", principalIDAttribute)

	expectedGroupPrincipalID := authactions.FormatPrincipalID(authactions.ActiveDirectory, authactions.PrincipalTypeGroup, a.authConfig.Group)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, a.authConfig.Group, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(a.T(), err, "A group search should return the ID built from the %s attribute", principalIDAttribute)
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TestActiveDirectoryGroupBindingUsesGroupIDAttribute() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	groupPrincipalID := authactions.FormatPrincipalID(authactions.ActiveDirectory, authactions.PrincipalTypeGroup, a.authConfig.Group)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.clusterID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to bind group %s to cluster %s", groupPrincipalID, a.clusterID)

	for _, adUser := range a.authConfig.Users {
		userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: adUser.Username, Password: adUser.Password}, authactions.ActiveDirectory)
		require.NoError(a.T(), err, "Active Directory user %s should be able to log in", adUser.Username)

		require.NoError(a.T(), rbacapi.VerifyUserCanI(userClient, a.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true),
			"The binding on group %s should give its member %s access to cluster %s", groupPrincipalID, adUser.Username, a.clusterID)
	}
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TestActiveDirectoryNestedGroupBindingUsesGroupIDAttribute() {
	require.True(a.T(), a.client.Auth.ActiveDirectory.Config.Groups.NestedGroupMembershipEnabled, "Nested group membership must be enabled under activeDirectory.groups for this test")
	require.NotEmpty(a.T(), a.authConfig.NestedGroup, "The Active Directory configuration should name the top group of the nested hierarchy")
	require.NotEmpty(a.T(), a.authConfig.TripleNestedUsers, "The Active Directory configuration should name the users two levels below the top group")

	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(a.client, a.session, a.adminUser, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	topGroupPrincipalID := authactions.FormatPrincipalID(authactions.ActiveDirectory, authactions.PrincipalTypeGroup, a.authConfig.NestedGroup)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, a.clusterID, topGroupPrincipalID, rbac.ClusterMember.String())
	require.NoError(a.T(), err, "Failed to bind group %s to cluster %s", topGroupPrincipalID, a.clusterID)

	adUser := a.authConfig.TripleNestedUsers[0]
	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: adUser.Username, Password: adUser.Password}, authactions.ActiveDirectory)
	require.NoError(a.T(), err, "Active Directory user %s should be able to log in", adUser.Username)

	require.NoError(a.T(), rbacapi.VerifyUserCanI(userClient, a.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true),
		"The binding on group %s should reach %s two levels down the nested groups", topGroupPrincipalID, adUser.Username)
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TestActiveDirectoryTestAndApplyRejectsIDAttributeChangeWhileEnabled() {
	tests := []struct {
		name             string
		userIDAttribute  string
		groupIDAttribute string
		message          string
	}{
		{"Changed user attribute", principalIDChangedAttribute, principalIDAttribute, "userIDAttribute cannot be changed on an enabled provider"},
		{"Cleared user attribute", "", principalIDAttribute, "userIDAttribute cannot be changed on an enabled provider"},
		{"Changed group attribute", principalIDAttribute, principalIDChangedAttribute, "groupIDAttribute cannot be changed on an enabled provider"},
		{"Cleared group attribute", principalIDAttribute, "", "groupIDAttribute cannot be changed on an enabled provider"},
	}

	for _, tt := range tests {
		a.Run(tt.name, func() {
			err := authactions.ActiveDirectoryTestAndApply(a.client, tt.userIDAttribute, tt.groupIDAttribute)
			assert.NoError(a.T(), authactions.VerifyAPIError(err, http.StatusUnprocessableEntity, "InvalidBodyContent", tt.message))
		})
	}

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err)
	require.Equal(a.T(), principalIDAttribute, userIDAttribute, "The rejected requests should leave userIDAttribute unchanged")
	require.Equal(a.T(), principalIDAttribute, groupIDAttribute, "The rejected requests should leave groupIDAttribute unchanged")
}

func (a *ActiveDirectoryPrincipalIDTestSuite) TestActiveDirectoryWebhookRejectsIDAttributeChangeWhileEnabled() {
	tests := []struct {
		name  string
		patch map[string]any
	}{
		{"Changed user attribute", map[string]any{authconfigsapi.UserIDAttributeField: principalIDChangedAttribute}},
		{"Cleared user attribute", map[string]any{authconfigsapi.UserIDAttributeField: ""}},
		{"Changed group attribute", map[string]any{authconfigsapi.GroupIDAttributeField: principalIDChangedAttribute}},
		{"Cleared group attribute", map[string]any{authconfigsapi.GroupIDAttributeField: ""}},
	}

	for _, tt := range tests {
		a.Run(tt.name, func() {
			err := authconfigsapi.PatchAuthConfig(a.client, authactions.ActiveDirectory, tt.patch)
			assert.True(a.T(), apierrors.IsBadRequest(err), "The webhook should reject the change as a bad request, got: %v", err)
			assert.ErrorContains(a.T(), err, "field is immutable when the provider is enabled")
		})
	}

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(a.client, authactions.ActiveDirectory)
	require.NoError(a.T(), err)
	require.Equal(a.T(), principalIDAttribute, userIDAttribute, "The rejected patches should leave userIDAttribute unchanged")
	require.Equal(a.T(), principalIDAttribute, groupIDAttribute, "The rejected patches should leave groupIDAttribute unchanged")
}

func TestActiveDirectoryPrincipalIDTestSuite(t *testing.T) {
	suite.Run(t, new(ActiveDirectoryPrincipalIDTestSuite))
}

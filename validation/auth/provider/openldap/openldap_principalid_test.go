//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package openldap

import (
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/go-ldap/ldap/v3"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extuserapi "github.com/rancher/shepherd/extensions/kubeapi/users"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	authconfigsapi "github.com/rancher/tests/actions/kubeapi/authconfigs"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	usersapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/kubeapi/workloads/pods"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/interoperability/ldapdirectory"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	principalIDUserAttribute    = "uid"
	principalIDGroupAttribute   = "cn"
	principalIDChangedAttribute = "entryUUID"
	principalIDLoginAttribute   = "cn"
	groupSearchFilterField      = "groupSearchFilter"
	enabledField                = "enabled"
)

type OpenLDAPPrincipalIDTestSuite struct {
	suite.Suite
	session         *session.Session
	client          *rancher.Client
	clusterID       string
	adminUser       *v3.User
	directory       *ldap.Conn
	userSearchBase  string
	groupSearchBase string
}

func (o *OpenLDAPPrincipalIDTestSuite) TearDownSuite() {
	o.session.Cleanup()
}

func (o *OpenLDAPPrincipalIDTestSuite) SetupSuite() {
	o.session = session.NewSession()

	client, err := rancher.NewClient("", o.session)
	require.NoError(o.T(), err, "Failed to create Rancher client")
	o.client = client

	supported, err := actionsClusters.IsRancherVersionAbove(o.client, "v2.16.0-0")
	require.NoError(o.T(), err, "Failed to read the Rancher server version")
	if !supported {
		o.T().Skip("Configurable LDAP principal ID attributes are only in Rancher v2.16 and later")
	}

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(o.T(), clusterName, "Cluster name should be set")
	o.clusterID, err = clusters.GetClusterIDByName(o.client, clusterName)
	require.NoError(o.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	o.adminUser = &v3.User{
		Username: client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: client.Auth.OLDAP.Config.Users.Admin.Password,
	}
	o.userSearchBase = client.Auth.OLDAP.Config.Users.SearchBase
	o.groupSearchBase = client.Auth.OLDAP.Config.Groups.SearchBase

	o.directory, err = ldapdirectory.Dial(o.session, o.client)
	require.NoError(o.T(), err, "Failed to connect to the OpenLDAP directory")

	restore, err := authactions.CaptureOpenLDAPRestore(o.client)
	require.NoError(o.T(), err, "Failed to record the OpenLDAP settings to restore")
	o.session.RegisterCleanupFunc(restore)

	o.T().Log("Disabling OpenLDAP so it can be enabled with principal ID attributes")
	require.NoError(o.T(), authactions.DisableAuthProviderAndWait(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	o.T().Logf("Enabling OpenLDAP with userIDAttribute %s and groupIDAttribute %s", principalIDUserAttribute, principalIDGroupAttribute)
	require.NoError(o.T(), authactions.EnableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute), "Failed to enable OpenLDAP with principal ID attributes")
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPLoginUsesUserIDAttribute() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	_, err = ldapdirectory.CreateUser(subSession, o.directory, ou, uid, password)
	require.NoError(o.T(), err)

	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	user, err := extuserapi.GetUserByName(authAdmin, userClient.UserID)
	require.NoError(o.T(), err)

	expectedPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, uid)
	require.Contains(o.T(), user.PrincipalIDs, expectedPrincipalID, "The login should identify the user by its %s attribute", principalIDUserAttribute)

	for _, principalID := range user.PrincipalIDs {
		if strings.HasPrefix(principalID, authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, "")) {
			require.Equal(o.T(), expectedPrincipalID, principalID, "The user should carry no DN-based OpenLDAP principal")
		}
	}
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPPrincipalSearchReturnsIDAttributeValues() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)
	groupOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.groupSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	userDN, err := ldapdirectory.CreateUser(subSession, o.directory, userOU, uid, namegen.AppendRandomString("Pid-pass"))
	require.NoError(o.T(), err)

	groupCN := namegen.AppendRandomString("pid-group")
	_, err = ldapdirectory.CreateGroup(subSession, o.directory, groupOU, groupCN, []string{userDN})
	require.NoError(o.T(), err)

	expectedUserPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, uid)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, uid, authactions.PrincipalTypeUser, expectedUserPrincipalID)
	require.NoError(o.T(), err, "A user search should return the ID built from the %s attribute", principalIDUserAttribute)

	expectedGroupPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeGroup, groupCN)
	err = authactions.VerifyPrincipalSearchByTypeReturnsOnly(authAdmin, groupCN, authactions.PrincipalTypeGroup, expectedGroupPrincipalID)
	require.NoError(o.T(), err, "A group search should return the ID built from the %s attribute", principalIDGroupAttribute)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPUserMovedToAnotherOUKeepsIdentityAndBinding() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	engOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid-eng"))
	require.NoError(o.T(), err)
	opsOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid-ops"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(subSession, o.directory, engOU, uid, password)
	require.NoError(o.T(), err)

	firstLogin, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	_, err = rbacapi.CreateClusterRoleTemplateBinding(authAdmin, o.clusterID, firstLogin.UserID, rbac.ClusterMember.String())
	require.NoError(o.T(), err, "Failed to bind user %s to cluster %s", firstLogin.UserID, o.clusterID)

	require.NoError(o.T(), rbacapi.VerifyUserCanI(firstLogin, o.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true), "The user binding should give user %s access to cluster %s before the move", firstLogin.UserID, o.clusterID)

	movedDN, err := ldapdirectory.MoveEntry(subSession, o.directory, userDN, opsOU)
	require.NoError(o.T(), err)
	require.NotEqual(o.T(), userDN, movedDN, "Moving the user to another OU should change its DN")

	secondLogin, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should still log in after moving to another OU", uid)
	require.Equal(o.T(), firstLogin.UserID, secondLogin.UserID, "The login after the move should land on the same Rancher user")

	require.NoError(o.T(), rbacapi.VerifyUserCanI(secondLogin, o.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true), "The user binding should still give user %s access to cluster %s after the move", secondLogin.UserID, o.clusterID)

	expectedPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, uid)
	holders, err := usersapi.ListUsersByPrincipalID(o.client, expectedPrincipalID)
	require.NoError(o.T(), err)
	require.Len(o.T(), holders, 1, "Exactly one Rancher user should hold principal %s after the move", expectedPrincipalID)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPGroupMovedToAnotherOUKeepsBinding() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)
	firstGroupOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.groupSearchBase, namegen.AppendRandomString("pid-eng"))
	require.NoError(o.T(), err)
	secondGroupOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.groupSearchBase, namegen.AppendRandomString("pid-ops"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(subSession, o.directory, userOU, uid, password)
	require.NoError(o.T(), err)

	groupCN := namegen.AppendRandomString("pid-group")
	groupDN, err := ldapdirectory.CreateGroup(subSession, o.directory, firstGroupOU, groupCN, []string{userDN})
	require.NoError(o.T(), err)

	groupPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeGroup, groupCN)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, o.clusterID, groupPrincipalID, rbac.ClusterMember.String())
	require.NoError(o.T(), err, "Failed to bind group %s to cluster %s", groupPrincipalID, o.clusterID)

	firstLogin, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	require.NoError(o.T(), rbacapi.VerifyUserCanI(firstLogin, o.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true), "The binding on group %s should give its member access to cluster %s before the move", groupPrincipalID, o.clusterID)

	movedDN, err := ldapdirectory.MoveEntry(subSession, o.directory, groupDN, secondGroupOU)
	require.NoError(o.T(), err)
	require.NotEqual(o.T(), groupDN, movedDN, "Moving the group to another OU should change its DN")

	secondLogin, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should still log in after its group moved", uid)

	require.NoError(o.T(), rbacapi.VerifyUserCanI(secondLogin, o.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true), "The binding on group %s should still give its member access to cluster %s after the group moved", groupPrincipalID, o.clusterID)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPNestedGroupBindingUsesGroupIDAttribute() {
	require.True(o.T(), o.client.Auth.OLDAP.Config.Groups.NestedGroupMembershipEnabled, "Nested group membership must be enabled under openLDAP.groups for this test")

	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	userOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)
	groupOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.groupSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(subSession, o.directory, userOU, uid, password)
	require.NoError(o.T(), err)

	childDN, err := ldapdirectory.CreateGroup(subSession, o.directory, groupOU, namegen.AppendRandomString("pid-child"), []string{userDN})
	require.NoError(o.T(), err)

	parentCN := namegen.AppendRandomString("pid-parent")
	_, err = ldapdirectory.CreateGroup(subSession, o.directory, groupOU, parentCN, []string{childDN})
	require.NoError(o.T(), err)

	parentPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeGroup, parentCN)
	_, err = rbacapi.CreateGroupClusterRoleTemplateBinding(authAdmin, o.clusterID, parentPrincipalID, rbac.ClusterMember.String())
	require.NoError(o.T(), err, "Failed to bind parent group %s to cluster %s", parentPrincipalID, o.clusterID)

	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	require.NoError(o.T(), rbacapi.VerifyUserCanI(userClient, o.clusterID, rbacapi.ListVerb, "", rbacapi.NodesResource, "", "", true), "The binding on parent group %s should reach a member of its nested child group", parentPrincipalID)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPTestAndApplyRejectsIDAttributeChangeWhileEnabled() {
	tests := []struct {
		name             string
		userIDAttribute  string
		groupIDAttribute string
		message          string
	}{
		{"Changed user attribute", principalIDChangedAttribute, principalIDGroupAttribute, "userIDAttribute cannot be changed on an enabled provider"},
		{"Cleared user attribute", "", principalIDGroupAttribute, "userIDAttribute cannot be changed on an enabled provider"},
		{"Changed group attribute", principalIDUserAttribute, principalIDChangedAttribute, "groupIDAttribute cannot be changed on an enabled provider"},
		{"Cleared group attribute", principalIDUserAttribute, "", "groupIDAttribute cannot be changed on an enabled provider"},
	}

	for _, tt := range tests {
		o.Run(tt.name, func() {
			err := authactions.OpenLDAPTestAndApply(o.client, "", tt.userIDAttribute, tt.groupIDAttribute)
			assert.NoError(o.T(), authactions.VerifyAPIError(err, http.StatusUnprocessableEntity, "InvalidBodyContent", tt.message))
		})
	}

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.Equal(o.T(), principalIDUserAttribute, userIDAttribute, "The rejected requests should leave userIDAttribute unchanged")
	require.Equal(o.T(), principalIDGroupAttribute, groupIDAttribute, "The rejected requests should leave groupIDAttribute unchanged")
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPTestAndApplyAcceptsUnchangedIDAttributesWhileEnabled() {
	err := authactions.OpenLDAPTestAndApply(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	require.NoError(o.T(), err, "Re-submitting the enabled provider with the same principal ID attributes should be accepted")

	enabled, err := authconfigsapi.AuthConfigEnabled(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.True(o.T(), enabled, "OpenLDAP should still be enabled after the re-submit")

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.Equal(o.T(), principalIDUserAttribute, userIDAttribute)
	require.Equal(o.T(), principalIDGroupAttribute, groupIDAttribute)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPWebhookRejectsIDAttributeChangeWhileEnabled() {
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
		o.Run(tt.name, func() {
			err := authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, tt.patch)
			assert.True(o.T(), apierrors.IsBadRequest(err), "The webhook should reject the change as a bad request, got: %v", err)
			assert.ErrorContains(o.T(), err, "field is immutable when the provider is enabled")
		})
	}

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.Equal(o.T(), principalIDUserAttribute, userIDAttribute, "The rejected patches should leave userIDAttribute unchanged")
	require.Equal(o.T(), principalIDGroupAttribute, groupIDAttribute, "The rejected patches should leave groupIDAttribute unchanged")
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPWebhookAcceptsOtherFieldChangeWhileEnabled() {
	subSession := o.session.NewSession()
	defer subSession.Cleanup()

	originalFilter, filterSet, err := authconfigsapi.FindAuthConfigField(o.client, authactions.OpenLdap, groupSearchFilterField)
	require.NoError(o.T(), err)

	err = authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, map[string]any{groupSearchFilterField: "(objectClass=groupOfNames)"})
	require.NoError(o.T(), err, "Changing a field other than the principal ID attributes should be accepted while OpenLDAP is enabled")

	subSession.RegisterCleanupFunc(func() error {
		var restored any
		if filterSet {
			restored = originalFilter
		}

		return authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, map[string]any{groupSearchFilterField: restored})
	})

	userIDAttribute, groupIDAttribute, err := authconfigsapi.FindAuthConfigIDAttributes(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.Equal(o.T(), principalIDUserAttribute, userIDAttribute)
	require.Equal(o.T(), principalIDGroupAttribute, groupIDAttribute)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPRejectsInvalidIDAttributeNames() {
	subSession := o.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReenableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	})

	require.NoError(o.T(), authactions.DisableAuthProviderAndWait(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	webhookTests := []struct {
		name  string
		patch map[string]any
		field string
	}{
		{"Invalid user attribute when enabling through the Kubernetes API", map[string]any{enabledField: true, authconfigsapi.UserIDAttributeField: "1foo"}, authconfigsapi.UserIDAttributeField},
		{"Invalid group attribute when enabling through the Kubernetes API", map[string]any{enabledField: true, authconfigsapi.GroupIDAttributeField: "bad attr"}, authconfigsapi.GroupIDAttributeField},
	}

	for _, tt := range webhookTests {
		o.Run(tt.name, func() {
			err := authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, tt.patch)
			assert.True(o.T(), apierrors.IsBadRequest(err), "The webhook should reject enabling with an invalid name as a bad request, got: %v", err)
			assert.ErrorContains(o.T(), err, tt.field+": Forbidden: invalid value")
		})
	}

	apiTests := []struct {
		name             string
		userIDAttribute  string
		groupIDAttribute string
		message          string
	}{
		{"Invalid user attribute through testAndApply", "1foo", principalIDGroupAttribute, "invalid userIDAttribute"},
		{"Invalid group attribute through testAndApply", principalIDUserAttribute, "bad attr", "invalid groupIDAttribute"},
	}

	for _, tt := range apiTests {
		o.Run(tt.name, func() {
			err := authactions.OpenLDAPTestAndApply(o.client, "", tt.userIDAttribute, tt.groupIDAttribute)
			assert.NoError(o.T(), authactions.VerifyAPIError(err, http.StatusUnprocessableEntity, "InvalidBodyContent", tt.message))
		})
	}

	enabled, err := authconfigsapi.AuthConfigEnabled(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.False(o.T(), enabled, "The rejected requests should leave OpenLDAP disabled")
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPIDAttributesChangeWhileDisabled() {
	subSession := o.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReenableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	})

	require.NoError(o.T(), authactions.DisableAuthProviderAndWait(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	err := authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, map[string]any{authconfigsapi.UserIDAttributeField: principalIDChangedAttribute})
	require.NoError(o.T(), err, "Changing userIDAttribute should be accepted while OpenLDAP is disabled")

	storedUserIDAttribute, _, err := authconfigsapi.FindAuthConfigIDAttributes(o.client, authactions.OpenLdap)
	require.NoError(o.T(), err)
	require.Equal(o.T(), principalIDChangedAttribute, storedUserIDAttribute, "The disabled auth config should store the new userIDAttribute")

	err = authactions.EnableOpenLDAPWithIDAttributes(o.client, "", principalIDChangedAttribute, principalIDGroupAttribute)
	require.NoError(o.T(), err, "Enabling OpenLDAP again with a different userIDAttribute should be accepted")

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, subSession, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(authSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(authSession, o.directory, ou, uid, password)
	require.NoError(o.T(), err)

	entryUUID, err := ldapdirectory.FindAttribute(o.directory, userDN, principalIDChangedAttribute)
	require.NoError(o.T(), err)

	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	user, err := extuserapi.GetUserByName(authAdmin, userClient.UserID)
	require.NoError(o.T(), err)
	require.Contains(o.T(), user.PrincipalIDs, authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, entryUUID),
		"A login after re-enabling should identify the user by the new %s attribute", principalIDChangedAttribute)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPLoginFailsWithoutUserIDAttribute() {
	subSession := o.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReenableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	})

	require.NoError(o.T(), authactions.DisableAuthProviderAndWait(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	err := authactions.EnableOpenLDAPWithIDAttributes(o.client, principalIDLoginAttribute, principalIDUserAttribute, principalIDGroupAttribute)
	require.NoError(o.T(), err, "Failed to enable OpenLDAP with login attribute %s", principalIDLoginAttribute)

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, subSession, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(authSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	name := namegen.AppendRandomString("pid-noid")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(authSession, o.directory, ou, name, password)
	require.NoError(o.T(), err)

	require.NoError(o.T(), ldapdirectory.RemoveAttribute(o.directory, userDN, principalIDUserAttribute), "Failed to remove %s from %s", principalIDUserAttribute, userDN)

	_, err = authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: name, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), authactions.VerifyLoginRefused(err, http.StatusInternalServerError), "A user whose entry has no %s should not be able to log in", principalIDUserAttribute)

	dnPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, userDN)
	dnUsers, err := usersapi.ListUsersByPrincipalID(o.client, dnPrincipalID)
	require.NoError(o.T(), err)
	require.Empty(o.T(), dnUsers, "Rancher should not fall back to a DN-based principal for %s", userDN)

	logsByPod, err := pods.GetPodLogsFromDeployment(o.client, extclusterapi.LocalCluster, rbac.RancherDeploymentNamespace, rbac.RancherDeploymentName)
	require.NoError(o.T(), err, "Failed to read the Rancher logs")

	expectedLog := fmt.Sprintf("configured identifier attribute %q not found on entry %s", principalIDUserAttribute, userDN)
	require.True(o.T(), slices.ContainsFunc(slices.Collect(maps.Values(logsByPod)), func(logs string) bool { return strings.Contains(logs, expectedLog) }),
		"The Rancher logs should record the missing identifier attribute for %s", userDN)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPDuplicateUserIDAttributeValueFailsLookup() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	firstOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid-eng"))
	require.NoError(o.T(), err)
	secondOU, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid-ops"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-dup")
	password := namegen.AppendRandomString("Pid-pass")
	_, err = ldapdirectory.CreateUser(subSession, o.directory, firstOU, uid, password)
	require.NoError(o.T(), err)
	_, err = ldapdirectory.CreateUser(subSession, o.directory, secondOU, uid, password)
	require.NoError(o.T(), err)

	principalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, uid)
	err = authactions.VerifyPrincipalByID(authAdmin, principalID, authactions.OpenLdap, authactions.PrincipalTypeUser)
	require.NoError(o.T(), authactions.VerifyAPIError(err, http.StatusInternalServerError, "ServerError", fmt.Sprintf("ldap: multiple entries found for %s=%s", principalIDUserAttribute, uid)),
		"Looking up %s should fail while two entries share its %s", principalID, principalIDUserAttribute)

	_, err = authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), authactions.VerifyLoginRefused(err, http.StatusUnauthorized), "Logging in as %s should fail while two entries share its %s", uid, principalIDUserAttribute)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPRefreshMarksDeletedUserNotFound() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(subSession, o.directory, ou, uid, password)
	require.NoError(o.T(), err)

	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	require.NoError(o.T(), ldapdirectory.DeleteEntry(o.directory, userDN), "Failed to delete %s from the directory", userDN)
	require.NoError(o.T(), usersapi.TriggerUserAttributeRefresh(o.client, userClient.UserID), "Failed to trigger a refresh for %s", userClient.UserID)

	attribute, err := usersapi.WaitForUserAttributeAnnotation(o.client, userClient.UserID, usersapi.ProviderRefreshErrorAnnotation)
	require.NoError(o.T(), err, "The refresh should record that %s no longer exists", uid)
	require.Contains(o.T(), attribute.Annotations[usersapi.ProviderRefreshErrorAnnotation], fmt.Sprintf("%s=%s not found", principalIDUserAttribute, uid),
		"The refresh error should name the missing %s", principalIDUserAttribute)
	require.False(o.T(), attribute.NeedsRefresh, "The refresh should stop retrying once the user is reported not found")
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPDisableCleanupThenNewIDAttributeCreatesNewUsers() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(subSession, o.directory, ou, uid, password)
	require.NoError(o.T(), err)

	firstLogin, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	adminClient, err := o.client.WithSession(subSession)
	require.NoError(o.T(), err)

	crtb, err := rbacapi.CreateClusterRoleTemplateBinding(adminClient, o.clusterID, firstLogin.UserID, rbac.ClusterMember.String())
	require.NoError(o.T(), err, "Failed to bind user %s to cluster %s", firstLogin.UserID, o.clusterID)

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReenableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	})

	require.NoError(o.T(), authactions.DisableAuthProviderAndWait(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	require.NoError(o.T(), usersapi.WaitForUserDeletion(o.client, firstLogin.UserID), "Disabling with the default cleanup should remove user %s", firstLogin.UserID)

	_, err = o.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Get(crtb.Namespace, crtb.Name, metav1.GetOptions{})
	require.True(o.T(), apierrors.IsNotFound(err), "Disabling with the default cleanup should remove the binding of user %s, got: %v", firstLogin.UserID, err)

	err = authactions.EnableOpenLDAPWithIDAttributes(o.client, "", principalIDChangedAttribute, principalIDGroupAttribute)
	require.NoError(o.T(), err, "Enabling OpenLDAP with userIDAttribute %s after the cleanup should be accepted", principalIDChangedAttribute)

	authSession, newAuthAdmin, err := authactions.SetupAuthenticatedSession(o.client, subSession, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test after re-enabling")
	defer authSession.Cleanup()

	entryUUID, err := ldapdirectory.FindAttribute(o.directory, userDN, principalIDChangedAttribute)
	require.NoError(o.T(), err)

	secondLogin, err := authactions.LoginAsAuthUser(newAuthAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in after re-enabling", uid)
	require.NotEqual(o.T(), firstLogin.UserID, secondLogin.UserID, "The login after the cleanup should create a new Rancher user")

	user, err := extuserapi.GetUserByName(newAuthAdmin, secondLogin.UserID)
	require.NoError(o.T(), err)
	require.Contains(o.T(), user.PrincipalIDs, authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, entryUUID),
		"The new user should be identified by the new %s attribute", principalIDChangedAttribute)
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPUserLockedDisableKeepsUsersWithUnresolvablePrincipals() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, o.session, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer subSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(subSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	_, err = ldapdirectory.CreateUser(subSession, o.directory, ou, uid, password)
	require.NoError(o.T(), err)

	firstLogin, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	adminClient, err := o.client.WithSession(subSession)
	require.NoError(o.T(), err)

	crtb, err := rbacapi.CreateClusterRoleTemplateBinding(adminClient, o.clusterID, firstLogin.UserID, rbac.ClusterMember.String())
	require.NoError(o.T(), err, "Failed to bind user %s to cluster %s", firstLogin.UserID, o.clusterID)

	originalCleanup, _, err := authconfigsapi.FindAuthConfigField(o.client, authactions.OpenLdap, "metadata", "annotations", authactions.AuthProvCleanupAnnotationKey)
	require.NoError(o.T(), err)

	err = authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, map[string]any{
		"metadata": map[string]any{"annotations": map[string]any{authactions.AuthProvCleanupAnnotationKey: authactions.AuthProvCleanupAnnotationValUserLocked}},
	})
	require.NoError(o.T(), err, "Failed to set the %s cleanup annotation", authactions.AuthProvCleanupAnnotationValUserLocked)

	subSession.RegisterCleanupFunc(func() error {
		err := authconfigsapi.PatchAuthConfig(o.client, authactions.OpenLdap, map[string]any{
			"metadata": map[string]any{"annotations": map[string]any{authactions.AuthProvCleanupAnnotationKey: originalCleanup}},
		})
		if err != nil {
			return err
		}

		return authactions.ReenableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	})

	require.NoError(o.T(), authactions.EnsureAuthProviderDisabled(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	require.NoError(o.T(), usersapi.VerifyUserRetained(o.client, firstLogin.UserID, defaults.OneMinuteTimeout),
		"Disabling with the %s annotation should keep user %s", authactions.AuthProvCleanupAnnotationValUserLocked, firstLogin.UserID)

	_, err = o.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Get(crtb.Namespace, crtb.Name, metav1.GetOptions{})
	require.NoError(o.T(), err, "Disabling with the %s annotation should keep the binding of user %s", authactions.AuthProvCleanupAnnotationValUserLocked, firstLogin.UserID)

	err = authactions.OpenLDAPTestAndApply(o.client, "", principalIDChangedAttribute, principalIDGroupAttribute)
	require.NoError(o.T(), err, "Enabling OpenLDAP with userIDAttribute %s should be accepted", principalIDChangedAttribute)

	oldPrincipalID := authactions.FormatPrincipalID(authactions.OpenLdap, authactions.PrincipalTypeUser, uid)
	keptUser, err := extuserapi.GetUserByName(o.client, firstLogin.UserID)
	require.NoError(o.T(), err)
	require.Contains(o.T(), keptUser.PrincipalIDs, oldPrincipalID, "The kept user should still hold its old principal ID")

	err = authactions.VerifyPrincipalByID(o.client, oldPrincipalID, authactions.OpenLdap, authactions.PrincipalTypeUser)
	require.NoError(o.T(), authactions.VerifyAPIError(err, http.StatusNotFound, "NotFound", ""), "The old principal ID %s should no longer resolve once OpenLDAP uses %s", oldPrincipalID, principalIDChangedAttribute)

	authSession, newAuthAdmin, err := authactions.SetupAuthenticatedSession(o.client, subSession, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test after re-enabling")
	defer authSession.Cleanup()

	secondLogin, err := authactions.LoginAsAuthUser(newAuthAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in after re-enabling", uid)
	require.NotEqual(o.T(), firstLogin.UserID, secondLogin.UserID, "The login with the new attribute should land on a new Rancher user, not the kept one")
}

func (o *OpenLDAPPrincipalIDTestSuite) TestOpenLDAPRefreshMarksDeletedUserNotFoundInDNMode() {
	subSession := o.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		return authactions.ReenableOpenLDAPWithIDAttributes(o.client, "", principalIDUserAttribute, principalIDGroupAttribute)
	})

	require.NoError(o.T(), authactions.DisableAuthProviderAndWait(o.client, authactions.OpenLdap), "Failed to disable OpenLDAP")

	require.NoError(o.T(), authactions.EnableOpenLDAPWithIDAttributes(o.client, "", "", ""), "Failed to enable OpenLDAP in DN mode")

	authSession, authAdmin, err := authactions.SetupAuthenticatedSession(o.client, subSession, o.adminUser, authactions.OpenLdap)
	require.NoError(o.T(), err, "Failed to setup authenticated test")
	defer authSession.Cleanup()

	ou, err := ldapdirectory.CreateOU(authSession, o.directory, o.userSearchBase, namegen.AppendRandomString("pid"))
	require.NoError(o.T(), err)

	uid := namegen.AppendRandomString("pid-user")
	password := namegen.AppendRandomString("Pid-pass")
	userDN, err := ldapdirectory.CreateUser(authSession, o.directory, ou, uid, password)
	require.NoError(o.T(), err)

	userClient, err := authactions.LoginAsAuthUser(authAdmin, &v3.User{Username: uid, Password: password}, authactions.OpenLdap)
	require.NoError(o.T(), err, "OpenLDAP user %s should be able to log in", uid)

	require.NoError(o.T(), ldapdirectory.DeleteEntry(o.directory, userDN), "Failed to delete %s from the directory", userDN)
	require.NoError(o.T(), usersapi.TriggerUserAttributeRefresh(o.client, userClient.UserID), "Failed to trigger a refresh for %s", userClient.UserID)

	attribute, err := usersapi.WaitForUserAttributeAnnotation(o.client, userClient.UserID, usersapi.ProviderRefreshErrorAnnotation)
	require.NoError(o.T(), err, "The refresh should record that %s no longer exists", userDN)
	require.Contains(o.T(), attribute.Annotations[usersapi.ProviderRefreshErrorAnnotation], fmt.Sprintf("%s not found", userDN),
		"The refresh error should name the missing DN")
	require.False(o.T(), attribute.NeedsRefresh, "The refresh should stop retrying once the user is reported not found")
}

func TestOpenLDAPPrincipalIDTestSuite(t *testing.T) {
	suite.Run(t, new(OpenLDAPPrincipalIDTestSuite))
}

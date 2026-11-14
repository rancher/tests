//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"
	usersapi "github.com/rancher/tests/actions/kubeapi/users"
	"github.com/rancher/tests/actions/rbac"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"k8s.io/client-go/kubernetes"
)

const (
	scimLoginUsersNeeded   = 2
	scimNoGroupUserMissing = "No noGroupUser is set under openLdapAuthInput, so there is no OpenLDAP user without a directory group to log in as"
)

type SCIMLoginAttributesTestSuite struct {
	suite.Suite
	session         *session.Session
	client          *rancher.Client
	scimClient      *scimclient.Client
	authUsers       []authactions.User
	noGroupUser     authactions.User
	userSearchBase  string
	groupSearchBase string
}

func (s *SCIMLoginAttributesTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Setting up SCIM client for OpenLDAP provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup SCIM client")
	s.scimClient = scimClient

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	servable, reason, err := scimactions.VerifySCIMServable(s.client)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server serves SCIM")
	if !servable {
		s.T().Skip(reason)
	}

	logrus.Info("Ensuring baseline SCIM ConfigMap (enabled=true) for OpenLDAP provider")
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK))

	authConfig := new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, authConfig)
	require.GreaterOrEqual(s.T(), len(authConfig.Users), scimLoginUsersNeeded, "At least %d auth users must be configured in cattle-config.yaml", scimLoginUsersNeeded)
	s.authUsers = authConfig.Users
	s.noGroupUser = authConfig.NoGroupUser

	s.userSearchBase = s.client.Auth.OLDAP.Config.Users.SearchBase
	require.NotEmpty(s.T(), s.userSearchBase, "The OpenLDAP user search base must be set in cattle-config.yaml")
	s.groupSearchBase = s.client.Auth.OLDAP.Config.Groups.SearchBase
}

func (s *SCIMLoginAttributesTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMUserKeepsExternalIDAfterLogin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)
	email := namegen.AppendRandomString("scim-mail") + "@example.com"

	logrus.Infof("Provisioning OpenLDAP user %s through SCIM as %s", ldapUser.Username, distinguishedName)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, email)
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	logrus.Infof("Logging in as OpenLDAP user %s", ldapUser.Username)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200 after login"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), externalID, body["externalId"], "Logging in should not remove the externalId SCIM stored")

	emails, _ := body["emails"].([]interface{})
	require.Len(s.T(), emails, 1, "Logging in should not remove the email SCIM stored")
	stored, _ := emails[0].(map[string]interface{})
	require.Equal(s.T(), email, stored["value"], "Logging in should keep the email value SCIM stored")

	storedExternalID, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{externalID}, storedExternalID, "The user attribute should still carry the externalId after login")

	storedEmail, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeEmailKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{email}, storedEmail, "The user attribute should still carry the email after login")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMUserStoredAttributesStayOutOfDownstreamRequests() {
	hasFix, err := actionsClusters.IsRancherVersionAbove(s.client, "v2.15.4-0")
	require.NoError(s.T(), err, "Failed to read the Rancher server version")
	if !hasFix {
		s.T().Skip("Known issue: https://github.com/rancher/rancher/issues/57655")
	}

	clusterID, err := clusters.GetClusterIDByName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err, "Failed to find the downstream cluster [%v]", s.client.RancherConfig.ClusterName)

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	restoreACE, err := authactions.EnsureACEEnabled(s.client, clusterID)
	if restoreACE != nil {
		subSession.RegisterCleanupFunc(restoreACE)
	}
	require.NoError(s.T(), err, "Failed to enable the authorized cluster endpoint on cluster [%v]", clusterID)

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)
	email := namegen.AppendRandomString("scim-mail") + "@example.com"

	logrus.Infof("Provisioning OpenLDAP user %s through SCIM with an externalId and an email", ldapUser.Username)
	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, email)
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = rbacapi.CreateClusterRoleTemplateBinding(subClient, clusterID, userID, rbac.ClusterMember.String())
	require.NoError(s.T(), err, "Failed to grant the SCIM user access to cluster [%v]", clusterID)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	generated, err := authactions.SyncedACEKubeconfig(s.client, authUser, clusterID)
	require.NoError(s.T(), err, "Failed to generate the SCIM user's kubeconfig for cluster [%v]", clusterID)

	replacedExternalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)
	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "externalId", replacedExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH /Users/{id} should write the externalId back after login"))

	storedExternalID, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{replacedExternalID}, storedExternalID, "The user attribute should carry the externalId, so there is an attribute beyond principalid and username to leave out")

	proxyConfig, err := authactions.ProxyRestConfig(generated)
	require.NoError(s.T(), err)

	proxyClientset, err := kubernetes.NewForConfig(proxyConfig)
	require.NoError(s.T(), err)

	requiredExtras := []string{scimactions.UserAttributePrincipalIDKey, scimactions.UserAttributeUserNameKey}
	requestExtras := append(slices.Clone(requiredExtras), scimactions.UserExtraRequestTokenIDKey, scimactions.UserExtraRequestHostKey)

	require.NoError(s.T(), authactions.VerifySelfSubjectReviewExtras(proxyClientset, userID, requiredExtras, requestExtras),
		"The Rancher proxy on cluster [%v] should send the SCIM user with only the defined extras, not its stored attributes", clusterID)

	attribute, err := authactions.WaitForClusterUserAttributeExtras(s.client, clusterID, userID)
	require.NoError(s.T(), err)

	require.Contains(s.T(), attribute.ExtraByProvider, authactions.OpenLdap, "The cluster user attribute should hold the OpenLDAP extras")
	require.Contains(s.T(), attribute.ExtraByProvider[authactions.OpenLdap], scimactions.UserAttributePrincipalIDKey, "The cluster user attribute should still hold the principalid")
	require.Contains(s.T(), attribute.ExtraByProvider[authactions.OpenLdap], scimactions.UserAttributeUserNameKey, "The cluster user attribute should still hold the username")
	for provider, extra := range attribute.ExtraByProvider {
		for key := range extra {
			require.Contains(s.T(), []string{scimactions.UserAttributePrincipalIDKey, scimactions.UserAttributeUserNameKey}, key,
				"The cluster user attribute on cluster [%v] should hold only principalid and username, not the stored %v attribute of %v", clusterID, key, provider)
		}
	}

	aceClientsets, err := authactions.ClientsetsFromKubeconfig(generated)
	require.NoError(s.T(), err)

	require.NotEmpty(s.T(), aceClientsets, "The kubeconfig should name an authorized cluster endpoint context")

	for name, clientset := range aceClientsets {
		require.NoError(s.T(), authactions.VerifySelfSubjectReviewExtras(clientset, userID, nil, requestExtras),
			"The authorized cluster endpoint context [%v] should resolve to the SCIM user with only the defined extras, not its stored attributes", name)
	}
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMCreateWritesEmptyEmailWithoutPrimaryEmail() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM create writes the email attribute even when the payload carries no primary email")

	_, withoutEmailID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), true)
	require.NoError(s.T(), err)

	storedEmpty, err := usersapi.GetUserExtraAttribute(s.client, withoutEmailID, authactions.OpenLdap, scimactions.UserAttributeEmailKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{""}, storedEmpty, "SCIM create should write an empty email rather than leave the attribute unset")

	email := namegen.AppendRandomString("scim-mail") + "@example.com"
	withEmailID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, namegen.AppendRandomString("scim-user"), namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), email)
	require.NoError(s.T(), err)

	storedEmail, err := usersapi.GetUserExtraAttribute(s.client, withEmailID, authactions.OpenLdap, scimactions.UserAttributeEmailKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{email}, storedEmail, "SCIM create should write the primary email the payload carries")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMFilterByExternalIDAfterLogin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	logrus.Infof("Verifying the externalId filter still finds OpenLDAP user %s after login", ldapUser.Username)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, distinguishedName),
		"Should be able to clear a leftover SCIM user for %s", distinguishedName)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("externalId eq %q", externalID))

	filterResp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(filterResp, http.StatusOK, "Filtered GET /Users should return 200"))

	body, err := scimactions.DecodeJSONBody(filterResp)
	require.NoError(s.T(), err)

	userResources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), userResources, 1, "The externalId filter should still return the user after login")

	found, _ := userResources[0].(map[string]interface{})
	require.Equal(s.T(), userID, found["id"], "The externalId filter should return the SCIM provisioned user")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMPutAfterLoginInExternalIDMode() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)

	logrus.Infof("Verifying PUT still updates OpenLDAP user %s after login when externalId is the principal identifier", ldapUser.Username)

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigTrue, scimactions.SCIMConfigUserIDAttributeKey: scimactions.SCIMConfigExternalIDValue}))

	userName := namegen.AppendRandomString("scim-user")
	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, userName, distinguishedName, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	putResp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   namegen.AppendRandomString("scim-renamed"),
		ExternalID: distinguishedName,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(putResp, http.StatusOK, "PUT /Users/{id} should return 200 after login when externalId is the principal identifier"))

	body, err := scimactions.DecodeJSONBody(putResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), distinguishedName, body["externalId"], "PUT should keep the externalId the login matched on")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMDisabledLoginWritesLoginNameAsUserName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)

	logrus.Infof("Verifying a login writes the login name of OpenLDAP user %s while SCIM is disabled for the provider", ldapUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), "")
	require.NoError(s.T(), err)

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigFalse}, http.StatusNotFound))

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login while SCIM is disabled", ldapUser.Username)

	stored, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeUserNameKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{ldapUser.Username}, stored, "With SCIM disabled the login should write the login name as it did before SCIM")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMUserNameSurvivesLoginInUserNameMode() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	logrus.Infof("Verifying a login keeps the userName SCIM stored for OpenLDAP user %s when userName is the principal identifier", ldapUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200 after login"))

	getBody, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), distinguishedName, getBody["userName"], "Logging in should not replace the userName SCIM stored with the login name")

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("userName eq %q", distinguishedName))

	filterResp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(filterResp, http.StatusOK, "Filtered GET /Users should return 200 after login"))

	filterBody, err := scimactions.DecodeJSONBody(filterResp)
	require.NoError(s.T(), err)

	filtered, _ := filterBody["Resources"].([]interface{})
	require.Len(s.T(), filtered, 1, "The userName filter should still find the user after login")

	putResp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   distinguishedName,
		ExternalID: externalID,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(putResp, http.StatusOK, "PUT /Users/{id} with the unchanged userName should return 200 after login"))
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMCreateForLoggedInUserLinksToSameUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[1]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	logrus.Infof("Logging in as OpenLDAP user %s before provisioning them through SCIM", ldapUser.Username)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	groupsAfterLogin, err := usersapi.GetUserGroupPrincipals(s.client, authUser.UserID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	logrus.Infof("OpenLDAP user %s holds %d group principals after login", ldapUser.Username, len(groupsAfterLogin))

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), authUser.UserID, userID, "SCIM should link to the user who logged in, not create a second one")

	groupsAfterSCIM, err := usersapi.GetUserGroupPrincipals(s.client, authUser.UserID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), groupsAfterLogin, groupsAfterSCIM, "SCIM creation should keep the groups the login stored")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMAttributesSurviveProviderRefresh() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	logrus.Infof("Verifying a provider refresh keeps what SCIM stored for OpenLDAP user %s", ldapUser.Username)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, distinguishedName),
		"Should be able to clear a leftover SCIM user for %s", distinguishedName)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	previousRefresh, err := usersapi.GetUserAttributeLastRefresh(s.client, userID)
	require.NoError(s.T(), err)

	logrus.Info("Triggering an auth provider refresh for all users")
	require.NoError(s.T(), users.RefreshGroupMembership(s.client))
	require.NoError(s.T(), usersapi.WaitForUserAttributeRefresh(s.client, userID, previousRefresh), "The provider refresher should write the user attribute of %s", userID)

	storedExternalID, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{externalID}, storedExternalID, "A provider refresh should not remove the externalId SCIM stored")

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200 after a provider refresh"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), externalID, body["externalId"], "SCIM should still report the externalId after a provider refresh")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMGroupMembershipReplacedByProviderGroupsOnLogin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)

	logrus.Infof("Verifying OpenLDAP groups replace the SCIM group membership of %s on login", ldapUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), "")
	require.NoError(s.T(), err)

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH add member should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))

	scimGroupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.OpenLdap, authactions.PrincipalTypeGroup, groupName)

	beforeLogin, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Contains(s.T(), beforeLogin, scimGroupPrincipal, "The SCIM group membership should be stored before login")

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	afterLogin, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), afterLogin, scimGroupPrincipal, "OpenLDAP sends groups, so login should replace the SCIM group membership with the directory groups")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMDisabledProviderRefreshRemovesStoredAttributes() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	logrus.Infof("Verifying a provider refresh removes what SCIM stored for OpenLDAP user %s while SCIM is disabled, as it did before SCIM", ldapUser.Username)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, distinguishedName),
		"Should be able to clear a leftover SCIM user for %s", distinguishedName)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	storedBefore, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{externalID}, storedBefore, "The externalId SCIM stored should be in place while SCIM is enabled")

	defer func() {
		require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap))
	}()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigFalse}, http.StatusNotFound))

	previousRefresh, err := usersapi.GetUserAttributeLastRefresh(s.client, userID)
	require.NoError(s.T(), err)

	logrus.Info("Triggering an auth provider refresh for all users")
	require.NoError(s.T(), users.RefreshGroupMembership(s.client))
	require.NoError(s.T(), usersapi.WaitForUserAttributeRefresh(s.client, userID, previousRefresh), "The provider refresher should write the user attribute of %s", userID)

	storedAfter, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Empty(s.T(), storedAfter, "With SCIM disabled the refresh should replace the provider's attributes and remove the externalId, as it did before SCIM")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMGroupMembershipEmptiedWhenProviderSendsNoGroups() {
	if s.noGroupUser.Username == "" {
		s.T().Skip(scimNoGroupUserMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, s.noGroupUser.Username, s.userSearchBase, s.groupSearchBase)

	logrus.Infof("Verifying a login by OpenLDAP user %s, who holds no directory group, empties the SCIM group membership rather than keeping it", s.noGroupUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), "")
	require.NoError(s.T(), err)

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH add member should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))

	scimGroupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.OpenLdap, authactions.PrincipalTypeGroup, groupName)

	beforeLogin, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Contains(s.T(), beforeLogin, scimGroupPrincipal, "The SCIM group membership should be stored before login")

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.noGroupUser.Username, Password: s.noGroupUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", s.noGroupUser.Username)

	afterLogin, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Empty(s.T(), afterLogin, "OpenLDAP is configured to send groups, so a login by a user who holds none should replace the SCIM group membership with an empty list, not keep it")
}

func TestSCIMLoginAttributesSuite(t *testing.T) {
	suite.Run(t, new(SCIMLoginAttributesTestSuite))
}

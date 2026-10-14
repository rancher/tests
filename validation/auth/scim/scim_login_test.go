//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	scimExternalIDAttribute = "externalid"
	scimEmailAttribute      = "email"
	scimLoginUsersNeeded    = 2
)

type SCIMLoginAttributesTestSuite struct {
	suite.Suite
	session         *session.Session
	client          *rancher.Client
	scimClient      *scimclient.Client
	authUsers       []authactions.User
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
	externalID := namegen.AppendRandomString("scim-ext")
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

	storedExternalID, err := scimactions.VerifyUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimExternalIDAttribute)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{externalID}, storedExternalID, "The user attribute should still carry the externalId after login")

	storedEmail, err := scimactions.VerifyUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimEmailAttribute)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{email}, storedEmail, "The user attribute should still carry the email after login")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMCreateWritesEmptyEmailWithoutPrimaryEmail() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM create writes the email attribute even when the payload carries no primary email")

	_, withoutEmailID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, namegen.AppendRandomString("scim-ext"), true)
	require.NoError(s.T(), err)

	storedEmpty, err := scimactions.VerifyUserExtraAttribute(s.client, withoutEmailID, authactions.OpenLdap, scimEmailAttribute)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{""}, storedEmpty, "SCIM create should write an empty email rather than leave the attribute unset")

	email := namegen.AppendRandomString("scim-mail") + "@example.com"
	withEmailID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, namegen.AppendRandomString("scim-user"), namegen.AppendRandomString("scim-ext"), email)
	require.NoError(s.T(), err)

	storedEmail, err := scimactions.VerifyUserExtraAttribute(s.client, withEmailID, authactions.OpenLdap, scimEmailAttribute)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{email}, storedEmail, "SCIM create should write the primary email the payload carries")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMFilterByExternalIDAfterLogin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString("scim-ext")

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

func (s *SCIMLoginAttributesTestSuite) TestSCIMCreateForLoggedInUserLinksToSameUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[1]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString("scim-ext")

	logrus.Infof("Logging in as OpenLDAP user %s before provisioning them through SCIM", ldapUser.Username)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	groupsAfterLogin, err := scimactions.VerifyUserGroupPrincipals(s.client, authUser.UserID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	logrus.Infof("OpenLDAP user %s holds %d group principals after login", ldapUser.Username, len(groupsAfterLogin))

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)
	require.Equal(s.T(), authUser.UserID, userID, "SCIM should link to the user who logged in, not create a second one")

	groupsAfterSCIM, err := scimactions.VerifyUserGroupPrincipals(s.client, authUser.UserID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.ElementsMatch(s.T(), groupsAfterLogin, groupsAfterSCIM, "SCIM creation should keep the groups the login stored")
}

func (s *SCIMLoginAttributesTestSuite) TestSCIMAttributesSurviveProviderRefresh() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	ldapUser := s.authUsers[0]
	distinguishedName := authactions.AuthUserDistinguishedName(authactions.OpenLdap, ldapUser.Username, s.userSearchBase, s.groupSearchBase)
	externalID := namegen.AppendRandomString("scim-ext")

	logrus.Infof("Verifying a provider refresh keeps what SCIM stored for OpenLDAP user %s", ldapUser.Username)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, distinguishedName),
		"Should be able to clear a leftover SCIM user for %s", distinguishedName)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	previousRefresh, err := scimactions.FetchUserAttributeLastRefresh(s.client, userID)
	require.NoError(s.T(), err)

	logrus.Info("Triggering an auth provider refresh for all users")
	require.NoError(s.T(), users.RefreshGroupMembership(s.client))
	require.NoError(s.T(), scimactions.WaitForUserAttributeRefresh(s.client, userID, previousRefresh), "The provider refresher should write the user attribute of %s", userID)

	storedExternalID, err := scimactions.VerifyUserExtraAttribute(s.client, userID, authactions.OpenLdap, scimExternalIDAttribute)
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

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, distinguishedName, namegen.AppendRandomString("scim-ext"), "")
	require.NoError(s.T(), err)

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH add member should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))

	scimGroupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.OpenLdap, authactions.PrincipalTypeGroup, groupName)

	beforeLogin, err := scimactions.VerifyUserGroupPrincipals(s.client, userID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Contains(s.T(), beforeLogin, scimGroupPrincipal, "The SCIM group membership should be stored before login")

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: ldapUser.Username, Password: ldapUser.Password}, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP user %s should be able to login", ldapUser.Username)

	afterLogin, err := scimactions.VerifyUserGroupPrincipals(s.client, userID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), afterLogin, scimGroupPrincipal, "OpenLDAP sends groups, so login should replace the SCIM group membership with the directory groups")
}

func TestSCIMLoginAttributesSuite(t *testing.T) {
	suite.Run(t, new(SCIMLoginAttributesTestSuite))
}

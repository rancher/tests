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
	"github.com/rancher/shepherd/extensions/clusters"
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
)

type SCIMKeycloakSAMLTestSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	cluster    *v3.Cluster
	scimClient *scimclient.Client
	samlUser   authactions.User
}

func (s *SCIMKeycloakSAMLTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(s.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(s.client, clusterName)
	require.NoError(s.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	s.cluster, err = s.client.Management.Cluster.ByID(clusterID)
	require.NoError(s.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	logrus.Info("Building the Keycloak SAML fixture")
	keycloakClient, err := authactions.NewKeycloakClient(s.session)
	require.NoError(s.T(), err, "Failed to reach Keycloak")

	fixture, err := authactions.SetupKeycloakSAML(s.client, keycloakClient)
	require.NoError(s.T(), err, "Failed to build the Keycloak SAML fixture")
	require.NotNil(s.T(), fixture.AuthInput, "The Keycloak SAML fixture should carry its auth input")
	require.NotEmpty(s.T(), fixture.AuthInput.Users, "The Keycloak SAML fixture should carry at least one account")
	s.samlUser = fixture.AuthInput.Users[0]

	logrus.Info("Disabling Keycloak SAML so it is enabled again with an empty groups field")
	keycloakConfig, err := s.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
	if err == nil && keycloakConfig.Enabled {
		require.NoError(s.T(), s.client.Auth.KeycloakSAML.Disable(), "Failed to disable Keycloak SAML before re-enabling it")
	}

	logrus.Info("Enabling Keycloak SAML with an empty groups field, so the provider sends no groups to Rancher")
	s.client.Auth.KeycloakSAML.Config.GroupsField = ""

	logrus.Info("Setting up SCIM client for the Keycloak SAML provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Failed to setup SCIM client for %s", authactions.KeycloakSAML)
	s.scimClient = scimClient

	servable, reason, err := scimactions.VerifySCIMServable(s.client)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server serves SCIM")
	if !servable {
		s.T().Skip(reason)
	}

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK),
		"The SCIM endpoint of the %s provider should answer 200", authactions.KeycloakSAML)
}

func (s *SCIMKeycloakSAMLTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMKeycloakSAMLTestSuite) TestSCIMKeycloakLoginKeepsSCIMGroupsWithEmptyGroupsField() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a SAML login with an empty groups field keeps the groups SCIM set for %s", s.samlUser.Username)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, s.samlUser.Username),
		"Should be able to clear a leftover SCIM user for %s", s.samlUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, s.samlUser.Username, namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), "")
	require.NoError(s.T(), err, "Should be able to provision %s through SCIM", s.samlUser.Username)

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH add member should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))

	groupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.KeycloakSAML, authactions.PrincipalTypeGroup, groupName)

	beforeLogin, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.KeycloakSAML)
	require.NoError(s.T(), err)
	require.Contains(s.T(), beforeLogin, groupPrincipal, "The SCIM group membership should be stored before login")

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	for attempt := 1; attempt <= 2; attempt++ {
		logrus.Infof("Logging in as %s through Keycloak SAML, attempt %d", s.samlUser.Username, attempt)

		authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.samlUser.Username, Password: s.samlUser.Password}, authactions.KeycloakSAML)
		require.NoError(s.T(), err, "Keycloak SAML user %s should be able to login", s.samlUser.Username)
		require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

		afterLogin, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.KeycloakSAML)
		require.NoError(s.T(), err)
		require.Contains(s.T(), afterLogin, groupPrincipal,
			"A login with an empty groups field should keep the SCIM group membership, attempt %d", attempt)
	}
}

func (s *SCIMKeycloakSAMLTestSuite) TestSCIMKeycloakRemovingGroupMemberRevokesAccessWithEmptyGroupsField() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying SCIM removing %s from a bound group revokes access even though login keeps stored groups", s.samlUser.Username)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, s.samlUser.Username),
		"Should be able to clear a leftover SCIM user for %s", s.samlUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, s.samlUser.Username, namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix), "")
	require.NoError(s.T(), err)

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	addResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(addResp, http.StatusOK, "PATCH add member should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))

	groupPrincipal := fmt.Sprintf("%s_%s://%s", authactions.KeycloakSAML, authactions.PrincipalTypeGroup, groupName)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(s.client, s.cluster.ID, groupPrincipal, string(rbac.ClusterMember))
	require.NoError(s.T(), err, "Should be able to bind the SCIM group to the cluster")
	require.NotEmpty(s.T(), crtb.Name)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	memberClient, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.samlUser.Username, Password: s.samlUser.Password}, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Keycloak SAML user %s should be able to login", s.samlUser.Username)

	clusterList, err := memberClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).ListAll(nil)
	require.NoError(s.T(), err, "The SCIM group member should reach the cluster the group is bound to")
	require.NotEmpty(s.T(), clusterList.Data, "The SCIM group member should see the cluster the group is bound to")

	logrus.Info("Removing the member through SCIM")
	removeResp, err := scimactions.PatchGroup(s.scimClient, groupID, "remove", fmt.Sprintf("members[value eq %q]", userID), nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(removeResp, http.StatusOK, "PATCH remove member should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 0))

	revokedClient, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.samlUser.Username, Password: s.samlUser.Password}, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Keycloak SAML user %s should still be able to login after the group removal", s.samlUser.Username)

	principals, err := usersapi.GetUserGroupPrincipals(s.client, userID, authactions.KeycloakSAML)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), principals, groupPrincipal, "SCIM removing the member should drop the group principal, kept groups must not survive a removal")

	_, err = revokedClient.Steve.SteveType(clusters.ProvisioningSteveResourceType).List(nil)
	require.Error(s.T(), err, "The removed member should no longer reach the cluster the group is bound to")
}

func (s *SCIMKeycloakSAMLTestSuite) TestSCIMKeycloakUserKeepsExternalIDAfterLogin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a Keycloak SAML login keeps the externalId SCIM stored for %s", s.samlUser.Username)

	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, s.samlUser.Username),
		"Should be able to clear a leftover SCIM user for %s", s.samlUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, s.samlUser.Username, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.samlUser.Username, Password: s.samlUser.Password}, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Keycloak SAML user %s should be able to login", s.samlUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200 after login"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), externalID, body["externalId"], "Logging in should not remove the externalId SCIM stored")

	stored, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.KeycloakSAML, scimactions.UserAttributeExternalIDKey)
	require.NoError(s.T(), err)
	require.Equal(s.T(), []string{externalID}, stored, "The user attribute should still carry the externalId after a SAML login")
}

func (s *SCIMKeycloakSAMLTestSuite) TestSCIMKeycloakPutAfterLoginKeepsUserName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying SCIM PUT still updates %s after a Keycloak SAML login", s.samlUser.Username)

	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, s.samlUser.Username),
		"Should be able to clear a leftover SCIM user for %s", s.samlUser.Username)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, s.samlUser.Username, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	_, err = authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.samlUser.Username, Password: s.samlUser.Password}, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Keycloak SAML user %s should be able to login", s.samlUser.Username)

	putResp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   s.samlUser.Username,
		ExternalID: externalID,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(putResp, http.StatusOK,
		"PUT /Users/{id} should return 200 after a SAML login, where the SCIM userName and the login name are the same value"))

	body, err := scimactions.DecodeJSONBody(putResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), externalID, body["externalId"], "PUT should keep the externalId")
}

type SCIMKeycloakUserNameFieldTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	scimClient    *scimclient.Client
	samlUser      authactions.User
	principalName string
}

func (s *SCIMKeycloakUserNameFieldTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	atLeast216, err := actionsClusters.IsRancherVersionAbove(s.client, scimactions.RancherV216Floor)
	require.NoError(s.T(), err, "Failed to read the Rancher server version")
	if !atLeast216 {
		s.T().Skip("Known issue: https://github.com/rancher/rancher/issues/58096")
	}

	logrus.Infof("Building the Keycloak SAML fixture with %q as the principal identifier and %q as the login name, so the two differ",
		authactions.KeycloakSAMLEmailField, authactions.KeycloakSAMLGivenNameField)
	restoreFields, err := authactions.SetKeycloakSAMLFields(s.client, authactions.KeycloakSAMLEmailField, authactions.KeycloakSAMLGivenNameField)
	require.NoError(s.T(), err, "Failed to switch the Keycloak SAML UID and username fields")
	s.session.RegisterCleanupFunc(restoreFields)

	keycloakClient, err := authactions.NewKeycloakClient(s.session)
	require.NoError(s.T(), err, "Failed to reach Keycloak")

	fixture, err := authactions.SetupKeycloakSAML(s.client, keycloakClient)
	require.NoError(s.T(), err, "Failed to build the Keycloak SAML fixture")
	require.NotNil(s.T(), fixture.AuthInput, "The Keycloak SAML fixture should carry its auth input")
	require.NotEmpty(s.T(), fixture.AuthInput.Users, "The Keycloak SAML fixture should carry at least one account")
	s.samlUser = fixture.AuthInput.Users[0]
	s.principalName = authactions.PrincipalNameOf(s.samlUser)
	require.NotEmpty(s.T(), s.principalName, "The Keycloak SAML fixture should carry the principal name of its first account")

	logrus.Info("Disabling Keycloak SAML so it is enabled again with the login name field this suite needs")
	keycloakConfig, err := s.client.Management.AuthConfig.ByID(authactions.KeycloakSAML)
	if err == nil && keycloakConfig.Enabled {
		require.NoError(s.T(), s.client.Auth.KeycloakSAML.Disable(), "Failed to disable Keycloak SAML before re-enabling it")
	}

	logrus.Info("Setting up SCIM client for the Keycloak SAML provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Failed to setup SCIM client for %s", authactions.KeycloakSAML)
	s.scimClient = scimClient

	servable, reason, err := scimactions.VerifySCIMServable(s.client)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server serves SCIM")
	if !servable {
		s.T().Skip(reason)
	}

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK),
		"The SCIM endpoint of the %s provider should answer 200", authactions.KeycloakSAML)
}

func (s *SCIMKeycloakUserNameFieldTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMKeycloakUserNameFieldTestSuite) TestSCIMKeycloakUserNameSurvivesLoginWhenLoginNameDiffers() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a Keycloak SAML login keeps the userName SCIM stored for %s when the provider sends a different login name", s.principalName)

	externalID := namegen.AppendRandomString(scimactions.SCIMExternalIDPrefix)

	require.NoError(s.T(), scimactions.DeleteSCIMUserByUserName(s.scimClient, s.principalName),
		"Should be able to clear a leftover SCIM user for %s", s.principalName)

	userID, err := scimactions.CreateSCIMUserForAuthUser(s.scimClient, subSession, s.principalName, externalID, "")
	require.NoError(s.T(), err)

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err)

	authUser, err := authactions.LoginAsAuthUser(subClient, &v3.User{Username: s.samlUser.Username, Password: s.samlUser.Password}, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Keycloak SAML user %s should be able to login", s.samlUser.Username)
	require.Equal(s.T(), userID, authUser.UserID, "The login should land on the SCIM provisioned user, not a second one")

	loginName, err := usersapi.GetUserExtraAttribute(s.client, userID, authactions.KeycloakSAML, scimactions.UserAttributeUserNameKey)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), loginName, "The provider must send a login name, otherwise this case never exercises a name that differs from the SCIM userName")
	require.NotEqual(s.T(), []string{s.principalName}, loginName,
		"The provider must send a login name that differs from the SCIM userName, otherwise this case is the same as the matching one")

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200 after login"))

	getBody, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), s.principalName, getBody["userName"], "Logging in should not replace the userName SCIM stored with the login name")

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("userName eq %q", s.principalName))

	filterResp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(filterResp, http.StatusOK, "Filtered GET /Users should return 200 after login"))

	filterBody, err := scimactions.DecodeJSONBody(filterResp)
	require.NoError(s.T(), err)

	filtered, _ := filterBody["Resources"].([]interface{})
	require.Len(s.T(), filtered, 1, "The userName filter should still find the user after login")

	putResp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   s.principalName,
		ExternalID: externalID,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(putResp, http.StatusOK, "PUT /Users/{id} with the unchanged userName should return 200 after login"))
}

func TestSCIMKeycloakSAMLSuite(t *testing.T) {
	suite.Run(t, new(SCIMKeycloakSAMLTestSuite))
}

func TestSCIMKeycloakUserNameFieldSuite(t *testing.T) {
	suite.Run(t, new(SCIMKeycloakUserNameFieldTestSuite))
}

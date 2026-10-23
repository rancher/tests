//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	extfeatures "github.com/rancher/shepherd/extensions/kubeapi/features"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"
	usersapi "github.com/rancher/tests/actions/kubeapi/users"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	multiProviderDisabled = "This Rancher server does not run with multiple auth providers enabled"
)

type SCIMMultiProviderTestSuite struct {
	suite.Suite
	session        *session.Session
	client         *rancher.Client
	ldapSCIMClient *scimclient.Client
	samlSCIMClient *scimclient.Client
}

func (s *SCIMMultiProviderTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	multiAuth, err := extfeatures.IsFeatureEnabled(s.client, authactions.MultiAuthProvidersFeatureFlag)
	require.NoError(s.T(), err, "Failed to read the %s feature", authactions.MultiAuthProvidersFeatureFlag)
	if !multiAuth {
		s.T().Skip(multiProviderDisabled)
	}

	logrus.Info("Building the Keycloak SAML fixture so a second provider can be enabled alongside OpenLDAP")
	keycloakClient, err := authactions.NewKeycloakClient(s.session)
	require.NoError(s.T(), err, "Failed to reach Keycloak")

	_, err = authactions.SetupKeycloakSAML(s.client, keycloakClient)
	require.NoError(s.T(), err, "Failed to build the Keycloak SAML fixture")

	logrus.Infof("Setting up SCIM for the %s provider", authactions.OpenLdap)
	s.ldapSCIMClient, err = scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup SCIM client for %s", authactions.OpenLdap)

	logrus.Infof("Setting up SCIM for the %s provider", authactions.KeycloakSAML)
	s.samlSCIMClient, err = scimactions.SetupSCIMClient(s.client, authactions.KeycloakSAML)
	require.NoError(s.T(), err, "Failed to setup SCIM client for %s", authactions.KeycloakSAML)

	servable, reason, err := scimactions.VerifySCIMServable(s.client)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server serves SCIM")
	if !servable {
		s.T().Skip(reason)
	}

	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.ldapSCIMClient, http.StatusOK),
		"The SCIM endpoint of the %s provider should answer 200", authactions.OpenLdap)
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.samlSCIMClient, http.StatusOK),
		"The SCIM endpoint of the %s provider should answer 200", authactions.KeycloakSAML)
}

func (s *SCIMMultiProviderTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMMultiProviderTestSuite) TestSCIMProviderCannotReachAnotherProviderUserByID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a %s token cannot reach a user the %s provider owns", authactions.OpenLdap, authactions.KeycloakSAML)

	_, samlUserID, err := scimactions.CreateSCIMUser(s.samlSCIMClient, subSession, "", true)
	require.NoError(s.T(), err, "Should be able to create a user through the %s provider", authactions.KeycloakSAML)

	results, err := scimactions.FetchSCIMUserByIDResults(s.ldapSCIMClient, samlUserID, namegen.AppendRandomString(scimHijackedUserPrefix))
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.VerifyByIDResultsStatus(results, http.StatusNotFound),
		"Every by-ID request from the %s provider should answer 404 for a %s user", authactions.OpenLdap, authactions.KeycloakSAML)

	survivor, err := s.samlSCIMClient.Users().ByID(samlUserID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(survivor, http.StatusOK,
		fmt.Sprintf("The %s user should survive the refused requests, including the DELETE", authactions.KeycloakSAML)))
}

func (s *SCIMMultiProviderTestSuite) TestSCIMProviderListExcludesAnotherProviderUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the %s user list carries its own user and no %s user", authactions.OpenLdap, authactions.KeycloakSAML)

	_, ldapUserID, err := scimactions.CreateSCIMUser(s.ldapSCIMClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, samlUserID, err := scimactions.CreateSCIMUser(s.samlSCIMClient, subSession, "", true)
	require.NoError(s.T(), err)

	ldapListed, err := scimactions.FetchSCIMUserIDs(s.ldapSCIMClient)
	require.NoError(s.T(), err)
	require.Contains(s.T(), ldapListed, ldapUserID, "The %s provider should list its own user", authactions.OpenLdap)
	require.NotContains(s.T(), ldapListed, samlUserID, "The %s provider should not list a %s user", authactions.OpenLdap, authactions.KeycloakSAML)

	samlListed, err := scimactions.FetchSCIMUserIDs(s.samlSCIMClient)
	require.NoError(s.T(), err)
	require.Contains(s.T(), samlListed, samlUserID, "The %s provider should list its own user", authactions.KeycloakSAML)
	require.NotContains(s.T(), samlListed, ldapUserID, "The %s provider should not list an %s user", authactions.KeycloakSAML, authactions.OpenLdap)
}

func (s *SCIMMultiProviderTestSuite) TestSCIMProviderCannotReachAnotherProviderGroupByID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a %s token cannot reach a group the %s provider owns", authactions.OpenLdap, authactions.KeycloakSAML)

	_, samlGroupID, err := scimactions.CreateSCIMGroup(s.samlSCIMClient, subSession, "")
	require.NoError(s.T(), err, "Should be able to create a group through the %s provider", authactions.KeycloakSAML)

	results, err := scimactions.FetchSCIMGroupByIDResults(s.ldapSCIMClient, samlGroupID, namegen.AppendRandomString(scimHijackedGroupPrefix))
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.VerifyByIDResultsStatus(results, http.StatusNotFound),
		"Every by-ID request from the %s provider should answer 404 for a %s group", authactions.OpenLdap, authactions.KeycloakSAML)

	survivor, err := s.samlSCIMClient.Groups().ByID(samlGroupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(survivor, http.StatusOK,
		fmt.Sprintf("The %s group should survive the refused requests, including the DELETE", authactions.KeycloakSAML)))
}

func (s *SCIMMultiProviderTestSuite) TestSCIMProviderCannotAddAnotherProviderUserToItsGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the %s provider cannot put a %s user in one of its groups", authactions.OpenLdap, authactions.KeycloakSAML)

	_, samlUserID, err := scimactions.CreateSCIMUser(s.samlSCIMClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, ldapGroupID, err := scimactions.CreateSCIMGroup(s.ldapSCIMClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.ldapSCIMClient, ldapGroupID, "add", "members", []scimclient.Member{{Value: samlUserID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusNotFound,
		fmt.Sprintf("Adding a %s user to an %s group should answer 404", authactions.KeycloakSAML, authactions.OpenLdap)))

	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.ldapSCIMClient, ldapGroupID, 0),
		"The refused member add should have written nothing to the group")

	principals, err := usersapi.GetUserGroupPrincipals(s.client, samlUserID, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Empty(s.T(), principals, "A %s user should hold no %s group principals", authactions.KeycloakSAML, authactions.OpenLdap)
}

func TestSCIMMultiProviderSuite(t *testing.T) {
	suite.Run(t, new(SCIMMultiProviderTestSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package scim

import (
	"fmt"
	"net/http"
	"net/url"

	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	authactions "github.com/rancher/tests/actions/auth"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

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

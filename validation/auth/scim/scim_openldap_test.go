//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"

	cattlev3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	extfeatures "github.com/rancher/shepherd/extensions/kubeapi/features"
	extrbacapi "github.com/rancher/shepherd/extensions/kubeapi/rbac"
	"github.com/rancher/shepherd/pkg/clientbase"
	authactions "github.com/rancher/tests/actions/auth"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"
	rbac "github.com/rancher/tests/actions/rbac"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type SCIMOpenLDAPTestSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	cluster    *v3.Cluster
	scimClient *scimclient.Client
}

func (s *SCIMOpenLDAPTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(s.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(s.client, clusterName)
	require.NoError(s.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	s.cluster, err = s.client.Management.Cluster.ByID(clusterID)
	require.NoError(s.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Setting up SCIM client for OpenLDAP provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup SCIM client")
	s.scimClient = scimClient

	logrus.Info("Ensuring baseline SCIM ConfigMap (enabled=true) for OpenLDAP provider")
	err = scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, map[string]string{"enabled": "true"})
	require.NoError(s.T(), err, "Failed to set baseline SCIM ConfigMap")
	err = scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK)
	require.NoError(s.T(), err, "SCIM endpoint should respond 200 after baseline ConfigMap set")
}

func (s *SCIMOpenLDAPTestSuite) TearDownSuite() {
	if s.client != nil {
		ldapConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
		if err == nil && ldapConfig.Enabled {
			logrus.Info("Disabling OpenLDAP authentication after test suite")
			err = s.client.Auth.OLDAP.Disable()
			if err != nil {
				logrus.WithError(err).Warn("Failed to disable OpenLDAP in teardown")
			}
		}
	}
	s.session.Cleanup()
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMFeatureFlagEnabled() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM feature flag is enabled")

	enabled, err := extfeatures.IsFeatureEnabled(s.client, scimactions.SCIMFeatureFlag)
	require.NoError(s.T(), err, "Failed to check SCIM feature flag state")
	require.True(s.T(), enabled, "SCIM feature flag should be enabled")

	resp, err := s.scimClient.Discovery().ServiceProviderConfig()
	require.NoError(s.T(), err, "GET /ServiceProviderConfig should not error")
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "ServiceProviderConfig should return 200"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMTokenSecretExistsInKubeAPI() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying SCIM token secret exists in %s via label selector for provider %s", scimactions.SCIMSecretNamespace, authactions.OpenLdap)

	token, err := scimactions.FetchSCIMBearerToken(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "SCIM token secret should exist in %s", scimactions.SCIMSecretNamespace)
	require.NotEmpty(s.T(), token, "SCIM bearer token should not be empty")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMZFeatureFlagDisableAndReenableEndpoint() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Disabling SCIM flag and verifying /Users returns 404")

	err := extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, false)
	require.NoError(s.T(), err, "Should be able to disable SCIM feature flag")

	err = scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound)
	require.NoError(s.T(), err, "GET /Users should return 404 when SCIM is disabled")

	logrus.Info("Re-enabling SCIM flag after test")
	err = extfeatures.UpdateFeatureFlag(s.client, scimactions.SCIMFeatureFlag, true)
	require.NoError(s.T(), err, "Should be able to re-enable SCIM feature flag")

	logrus.Info("Waiting for Rancher to be fully operational after restart")
	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after feature flag re-enable")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMServiceProviderConfig() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /ServiceProviderConfig response")

	resp, err := s.scimClient.Discovery().ServiceProviderConfig()
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "ServiceProviderConfig should return 200"))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
	require.NoError(s.T(), err, "Body should be valid JSON")
	require.Contains(s.T(), body, "schemas", "ServiceProviderConfig must have schemas field")
	require.Contains(s.T(), body, "patch", "ServiceProviderConfig must advertise patch support")
	require.Contains(s.T(), body, "filter", "ServiceProviderConfig must advertise filter support")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMResourceTypes() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /ResourceTypes lists Users and Groups")

	resp, err := s.scimClient.Discovery().ResourceTypes()
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "ResourceTypes should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)

	resourceTypes, ok := body["Resources"].([]interface{})
	require.True(s.T(), ok, "ResourceTypes response should have Resources array")
	require.GreaterOrEqual(s.T(), len(resourceTypes), 2, "Should have at least User and Group resource types")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMInvalidTokenReturns401() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying invalid bearer token returns 401")

	badClient := scimclient.NewClient(&clientbase.ClientOpts{
		URL:      fmt.Sprintf("https://%s", s.client.RancherConfig.Host),
		TokenKey: "invalid-token",
		Insecure: true,
	}, authactions.OpenLdap)

	resp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusUnauthorized, "Invalid token should return 401"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMListUsers() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users returns 200 with valid ListResponse")

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Users should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Contains(s.T(), body, "totalResults", "ListResponse should have totalResults")
	require.Contains(s.T(), body, "Resources", "ListResponse should have Resources array")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateAndGetUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Creating SCIM user with externalID")

	userName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "ext-"+namegen.AppendRandomString("id"), true)
	require.NoError(s.T(), err)

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), userName, body["userName"], "userName in response should match created value")
	require.Equal(s.T(), userID, body["id"], "id in response should match")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateDuplicateUserReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying duplicate userName returns 409")

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	resp2, err := s.scimClient.Users().Create(scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp2, http.StatusConflict, "Duplicate POST /Users should return 409"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMFilterUserByUserName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying filter by userName")

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("userName eq %q", userName))

	filterResp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(filterResp, http.StatusOK, "Filtered GET /Users should return 200"))

	body, err := scimactions.DecodeJSONBody(filterResp)
	require.NoError(s.T(), err)

	userResources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), userResources, 1, "Filter should return exactly 1 user")

	firstUser, _ := userResources[0].(map[string]interface{})
	require.Equal(s.T(), userName, firstUser["userName"], "Returned user userName should match filter")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchDeactivateUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH deactivate for user")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	getResp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Users/{id} should return 200 before PATCH"))

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH active=false should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), false, body["active"], "active should be false after deactivation")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchReactivateUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH reactivate for user")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	deactivateResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(deactivateResp, http.StatusOK, "PATCH active=false should return 200"))

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "active", true)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH active=true should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), true, body["active"], "active should be true after reactivation")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMDeleteUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying DELETE /Users/{id}")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	deleteResp, err := s.scimClient.Users().Delete(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(deleteResp, http.StatusNoContent, "DELETE /Users/{id} should return 204"))

	err = scimactions.WaitForSCIMResourceDeletion(func() (int, error) {
		resp, pollErr := s.scimClient.Users().ByID(userID)
		if pollErr != nil {
			return 0, nil
		}
		return resp.StatusCode, nil
	})
	require.NoError(s.T(), err, "User %s should return 404 after DELETE", userID)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGetNonExistentUserReturns404() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users/nonexistent-id returns 404")

	resp, err := s.scimClient.Users().ByID("nonexistent-id-99999")
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusNotFound, "GET non-existent user should return 404"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserPagination() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying /Users startIndex and count pagination parameters")

	params := url.Values{}
	params.Set("startIndex", "1")
	params.Set("count", "2")

	resp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "Paginated GET /Users should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Contains(s.T(), body, "totalResults", "Paginated response should include totalResults")
	require.Contains(s.T(), body, "startIndex", "Paginated response should echo startIndex")
	require.Contains(s.T(), body, "itemsPerPage", "Paginated response should include itemsPerPage")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMOutOfBoundsStartIndexReturnsEmpty() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying /Users?startIndex=9999 returns empty Resources, not 404")

	params := url.Values{}
	params.Set("startIndex", "9999")
	params.Set("count", "5")

	resp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "Out-of-bounds startIndex should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)

	userResources, _ := body["Resources"].([]interface{})
	require.Empty(s.T(), userResources, "Resources should be empty for out-of-bounds startIndex")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateAndGetGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Creating SCIM group")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	getResp, err := s.scimClient.Groups().ByID(groupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Groups/{id} should return 200"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), groupName, body["displayName"], "displayName should match")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateDuplicateGroupReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying duplicate group displayName returns 409")

	groupName, _, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	resp2, err := s.scimClient.Groups().Create(scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp2, http.StatusConflict, "Duplicate POST /Groups should return 409"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchAddMemberToGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH add member to group")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH add member should return 200"))

	err = scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1)
	require.NoError(s.T(), err, "Timed out waiting for group member to appear via GET")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchRemoveMemberFromGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH remove member from group")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	addResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(addResp, http.StatusOK, "PATCH add member should return 200"))

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "remove", fmt.Sprintf("members[value eq %q]", userID), nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH remove member should return 200"))

	err = scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 0)
	require.NoError(s.T(), err, "Timed out waiting for group member to be removed via GET")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGroupListExcludeMembersAttribute() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Groups?excludedAttributes=members omits members field")

	params := url.Values{}
	params.Set("excludedAttributes", "members")

	resp, err := s.scimClient.Groups().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Groups?excludedAttributes=members should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)

	groupResources, _ := body["Resources"].([]interface{})
	for _, rawGroup := range groupResources {
		group, _ := rawGroup.(map[string]interface{})
		_, hasMembersField := group["members"]
		require.False(s.T(), hasMembersField, "members field should be absent when excludedAttributes=members")
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCannotViewDefaultAdmin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying default admin user is not visible via SCIM (local users are not SCIM-provisioned)")

	params := url.Values{}
	params.Set("filter", `username eq "admin"`)

	resp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "Filter request should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)

	userResources, _ := body["Resources"].([]interface{})
	require.Empty(s.T(), userResources, "Local admin should not appear in SCIM — only SCIM-provisioned users are returned")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchNonExistentUserReturns404() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH active=false on non-existent SCIM user returns 404")

	patchResp, err := scimactions.PatchUser(s.scimClient, "nonexistent-admin-id", "replace", "active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusNotFound, "PATCH on non-existent user should return 404"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMOpenLDAPAuthUnaffectedBySCIM() {
	adminUser := &v3.User{
		Username: s.client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: s.client.Auth.OLDAP.Config.Users.Admin.Password,
	}
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(s.client, s.session, adminUser, authactions.OpenLdap)
	require.NoError(s.T(), err, "OpenLDAP auth session should succeed while SCIM is enabled")
	defer subSession.Cleanup()

	logrus.Info("Verifying OpenLDAP login works while SCIM feature flag is enabled")

	_, err = authAdmin.Management.User.List(nil)
	require.NoError(s.T(), err, "Authenticated OpenLDAP admin should be able to list users")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMLocalAuthUnaffectedBySCIM() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying local admin auth unaffected by SCIM feature flag")

	_, err := s.client.Management.User.List(nil)
	require.NoError(s.T(), err, "Local admin should be able to list users while SCIM is enabled")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMTokenSecretStillPresentAfterAuthTests() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM token secret is still present via kubeapi after auth regression tests")

	token, err := scimactions.FetchSCIMBearerToken(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "SCIM secret should still be present after auth regression tests")
	require.NotEmpty(s.T(), token, "SCIM bearer token should still be non-empty")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMAuthConfigEnabledViaSteve() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying OpenLDAP authconfig reports enabled=true")

	authConfig, err := s.client.Management.AuthConfig.ByID(authactions.OpenLdap)
	require.NoError(s.T(), err, "Should be able to GET openldap authconfig")
	require.True(s.T(), authConfig.Enabled, "OpenLDAP authconfig should report enabled=true")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMAuthUsersAsClusterMembers() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	authConfig := new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, authConfig)
	require.NotEmpty(s.T(), authConfig.Users, "At least one auth user must be configured in cattle-config.yaml")

	for _, authUser := range authConfig.Users {
		logrus.Infof("Creating cluster-member CRTB for auth user %s", authUser.Username)

		userV3 := &v3.User{Username: authUser.Username, Password: authUser.Password}
		authAdmin, err := authactions.LoginAsAuthUser(s.client, userV3, authactions.OpenLdap)
		require.NoError(s.T(), err, "Auth user %s should be able to login", authUser.Username)

		mgmtUser, err := s.client.Management.User.ByID(authAdmin.UserID)
		require.NoError(s.T(), err, "Should fetch Rancher user for auth user %s", authUser.Username)

		crtbObj := &cattlev3.ClusterRoleTemplateBinding{
			ObjectMeta:       metav1.ObjectMeta{Namespace: s.cluster.ID, GenerateName: "crtb-"},
			ClusterName:      s.cluster.ID,
			UserName:         mgmtUser.ID,
			RoleTemplateName: string(rbac.ClusterMember),
		}
		_, err = extrbacapi.CreateClusterRoleTemplateBinding(s.client, crtbObj)
		require.NoError(s.T(), err, "Should be able to create CRTB for auth user %s", authUser.Username)

		crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUser.ID, 1)
		require.NoError(s.T(), err, "Should find exactly 1 CRTB for auth user %s", authUser.Username)
		require.Equal(s.T(), s.cluster.ID, crtbs[0].ClusterName, "CRTB should be for the correct cluster")
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGroupRoleBindings() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying group CRTB for SCIM group")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	var userIDs []string
	for i := 0; i < 2; i++ {
		_, uid, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
		require.NoError(s.T(), err)
		userIDs = append(userIDs, uid)

		addResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: uid}})
		require.NoError(s.T(), err)
		require.NoError(s.T(), scimactions.CheckStatus(addResp, http.StatusOK, "PATCH add member to group should return 200"))
	}

	groupPrincipal := fmt.Sprintf("%s_group://%s", authactions.OpenLdap, groupName)
	logrus.Infof("Creating group CRTB for principal %s", groupPrincipal)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(s.client, s.cluster.ID, groupPrincipal, string(rbac.ClusterMember))
	require.NoError(s.T(), err, "Should be able to create group CRTB")
	require.NotEmpty(s.T(), crtb.Name)

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(s.client, groupPrincipal, s.cluster.ID)
	require.NoError(s.T(), err, "Should find CRTB for group %s", groupPrincipal)
	require.Equal(s.T(), groupPrincipal, foundCRTB.GroupPrincipalName)
	require.Equal(s.T(), s.cluster.ID, foundCRTB.ClusterName)
	require.Equal(s.T(), string(rbac.ClusterMember), foundCRTB.RoleTemplateName)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserPaginationMultiPage() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying multi-page pagination: creating 12 SCIM users")

	const numUsers = 12
	var createdIDs []string
	for i := 0; i < numUsers; i++ {
		_, uid, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
		require.NoError(s.T(), err)
		createdIDs = append(createdIDs, uid)
	}

	b1, err := scimactions.ListSCIMUsersPage(s.scimClient, 1, 5)
	require.NoError(s.T(), err)
	totalResults := int(b1["totalResults"].(float64))
	require.GreaterOrEqual(s.T(), totalResults, numUsers, "totalResults should be at least the number of users we created")
	require.Equal(s.T(), float64(1), b1["startIndex"], "startIndex should be 1")
	require.Equal(s.T(), float64(5), b1["itemsPerPage"], "page 1 should have 5 items")
	resources1, _ := b1["Resources"].([]interface{})
	require.Len(s.T(), resources1, 5, "page 1 should return exactly 5 users")

	b2, err := scimactions.ListSCIMUsersPage(s.scimClient, 6, 5)
	require.NoError(s.T(), err)
	require.Equal(s.T(), float64(totalResults), b2["totalResults"], "totalResults should be consistent across pages")
	require.Equal(s.T(), float64(6), b2["startIndex"])
	require.Equal(s.T(), float64(5), b2["itemsPerPage"])
	resources2, _ := b2["Resources"].([]interface{})
	require.Len(s.T(), resources2, 5, "page 2 should return exactly 5 users")

	b3, err := scimactions.ListSCIMUsersPage(s.scimClient, 11, totalResults)
	require.NoError(s.T(), err)
	require.Equal(s.T(), float64(totalResults), b3["totalResults"], "totalResults should be consistent on page 3")
	require.Equal(s.T(), float64(11), b3["startIndex"])
	resources3, _ := b3["Resources"].([]interface{})
	remaining := totalResults - 10
	require.Len(s.T(), resources3, remaining, "page 3 should return the remaining %d users", remaining)

	seen := map[string]bool{}
	for _, page := range [][]interface{}{resources1, resources2, resources3} {
		for _, item := range page {
			user, _ := item.(map[string]interface{})
			id, _ := user["id"].(string)
			require.False(s.T(), seen[id], "user id %s appears on multiple pages", id)
			seen[id] = true
		}
	}

	for _, id := range createdIDs {
		require.True(s.T(), seen[id], "created user %s should appear in paginated results", id)
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserRoleBindingsWork() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Creating SCIM user A with cluster-member CRTB")

	_, userIDA, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	mgmtUserA, err := s.client.Management.User.ByID(userIDA)
	require.NoError(s.T(), err, "Should be able to fetch Rancher user for SCIM user %s", userIDA)

	crtbObjA := &cattlev3.ClusterRoleTemplateBinding{
		ObjectMeta:       metav1.ObjectMeta{Namespace: s.cluster.ID, GenerateName: "crtb-"},
		ClusterName:      s.cluster.ID,
		UserName:         mgmtUserA.ID,
		RoleTemplateName: string(rbac.ClusterMember),
	}
	_, err = extrbacapi.CreateClusterRoleTemplateBinding(s.client, crtbObjA)
	require.NoError(s.T(), err, "Should be able to create CRTB for SCIM user %s", userIDA)

	crtbsA, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUserA.ID, 1)
	require.NoError(s.T(), err, "User A should have exactly 1 CRTB")
	require.Equal(s.T(), s.cluster.ID, crtbsA[0].ClusterName)
	require.Equal(s.T(), string(rbac.ClusterMember), crtbsA[0].RoleTemplateName)

	logrus.Info("Creating SCIM user B without any CRTB")

	_, userIDB, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	mgmtUserB, err := s.client.Management.User.ByID(userIDB)
	require.NoError(s.T(), err)

	_, err = rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUserB.ID, 0)
	require.NoError(s.T(), err, "User B should have no CRTBs")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUsersAsClusterMembers() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM users can be assigned as cluster members via CRTB")

	const numUsers = 3
	for i := 0; i < numUsers; i++ {
		logrus.Infof("Creating SCIM cluster member %d", i+1)

		_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
		require.NoError(s.T(), err)

		mgmtUser, err := s.client.Management.User.ByID(userID)
		require.NoError(s.T(), err, "Rancher user %s should exist after SCIM create", userID)

		crtbObj := &cattlev3.ClusterRoleTemplateBinding{
			ObjectMeta:       metav1.ObjectMeta{Namespace: s.cluster.ID, GenerateName: "crtb-"},
			ClusterName:      s.cluster.ID,
			UserName:         mgmtUser.ID,
			RoleTemplateName: string(rbac.ClusterMember),
		}
		_, err = extrbacapi.CreateClusterRoleTemplateBinding(s.client, crtbObj)
		require.NoError(s.T(), err, "Should be able to create CRTB for SCIM user %s", userID)
		_, err = rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUser.ID, 1)
		require.NoError(s.T(), err, "Should find exactly 1 CRTB for SCIM user %s", userID)
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMZZDisableAuthCleansUpGroupsAndToken() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Creating SCIM groups to verify they are deleted on auth provider disable")

	_, groupID1, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)
	_, groupID2, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)
	groupIDs := []string{groupID1, groupID2}

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	_, err = scimactions.FetchSCIMBearerToken(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Token secret should exist before disabling auth provider")

	logrus.Info("Disabling OpenLDAP auth provider to trigger SCIM cleanup")
	err = s.client.Auth.OLDAP.Disable()
	require.NoError(s.T(), err, "Should be able to disable OpenLDAP")

	logrus.Info("Waiting for SCIM groups to be deleted by cleanup controller")
	for _, gID := range groupIDs {
		err = scimactions.WaitForSCIMResourceDeletion(func() (int, error) {
			_, getErr := s.client.WranglerContext.Mgmt.Group().Get(gID, metav1.GetOptions{})
			if getErr != nil {
				return http.StatusNotFound, nil
			}
			return http.StatusOK, nil
		})
		require.NoError(s.T(), err, "SCIM group %s should be deleted after provider disable", gID)
	}

	logrus.Info("Verifying SCIM token secret is deleted")
	err = scimactions.WaitForSCIMResourceDeletion(func() (int, error) {
		_, fetchErr := scimactions.FetchSCIMBearerToken(s.client, authactions.OpenLdap)
		if fetchErr != nil {
			return http.StatusNotFound, nil
		}
		return http.StatusOK, nil
	})
	require.NoError(s.T(), err, "SCIM token secret should be deleted after provider disable")

	logrus.Infof("Verifying Rancher user %s is deleted when auth provider is disabled", userID)
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false,
		func(ctx context.Context) (bool, error) {
			_, err := s.client.Management.User.ByID(userID)
			return err != nil, nil
		},
	)
	require.NoError(s.T(), err, "Rancher user %s should be deleted when auth provider is disabled", userID)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMDeleteGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	deleteResp, err := s.scimClient.Groups().Delete(groupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(deleteResp, http.StatusNoContent, "DELETE /Groups/{id} should return 204"))

	err = scimactions.WaitForSCIMResourceDeletion(func() (int, error) {
		resp, pollErr := s.scimClient.Groups().ByID(groupID)
		if pollErr != nil {
			return 0, nil
		}
		return resp.StatusCode, nil
	})
	require.NoError(s.T(), err, "Group %s should return 404 after DELETE", groupID)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMFilterGroupByDisplayName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Groups?filter=displayName eq")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("displayName eq %q", groupName))

	filterResp, err := s.scimClient.Groups().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(filterResp, http.StatusOK, "Filtered GET /Groups should return 200"))

	body, err := scimactions.DecodeJSONBody(filterResp)
	require.NoError(s.T(), err)

	groupResources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), groupResources, 1, "Filter should return exactly 1 group")

	firstGroup, _ := groupResources[0].(map[string]interface{})
	require.Equal(s.T(), groupName, firstGroup["displayName"], "Returned group displayName should match filter")
	require.Equal(s.T(), groupID, firstGroup["id"])
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGetGroupByIDExcludeMembersAttribute() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	params := url.Values{}
	params.Set("excludedAttributes", "members")

	resp, err := s.scimClient.Groups().ByIDWithQuery(groupID, params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Groups/{id}?excludedAttributes=members should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), groupID, body["id"])
	_, hasMembersField := body["members"]
	require.False(s.T(), hasMembersField, "members field should be absent when excludedAttributes=members")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchGroupReplaceExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH replace externalId for group")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	newExternalID := "ext-" + groupName

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "replace", "externalId", newExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH replace externalId should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PATCH replace")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchGroupAddExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH op:add externalId for group is treated as replace")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	newExternalID := "ext-add-" + groupName

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "externalId", newExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH op:add externalId should return 200 (treated as replace)"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PATCH op:add")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchUserReplaceExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH replace externalId for user")

	userName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	newExternalID := "ext-" + userName

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "externalId", newExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH replace externalId should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PATCH replace")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchUserReplacePrimaryEmail() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH replace primary email for user")

	userName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	newEmail := userName + "@example.com"

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "emails[primary eq true].value", newEmail)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH replace primary email should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)

	emails, _ := body["emails"].([]interface{})
	require.Len(s.T(), emails, 1, "User should have exactly 1 email after PATCH")
	email, _ := emails[0].(map[string]interface{})
	require.Equal(s.T(), newEmail, email["value"], "Primary email value should be updated")
	require.Equal(s.T(), true, email["primary"], "Email should be marked as primary")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMResourceTypeByID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /ResourceTypes/{id} for User and Group")

	for _, resourceTypeID := range []string{"User", "Group"} {
		resp, err := s.scimClient.Discovery().ResourceTypeByID(resourceTypeID)
		require.NoError(s.T(), err)
		require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, fmt.Sprintf("GET /ResourceTypes/%s should return 200", resourceTypeID)))

		var body map[string]interface{}
		require.NoError(s.T(), resp.DecodeJSON(&body))
		require.Equal(s.T(), resourceTypeID, body["id"], "ResourceType id should match requested id")
		require.Equal(s.T(), resourceTypeID, body["name"])
	}

	notFoundResp, err := s.scimClient.Discovery().ResourceTypeByID("nonexistent")
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(notFoundResp, http.StatusNotFound, "GET /ResourceTypes/nonexistent should return 404"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMSchemas() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Schemas returns User and Group schemas")

	resp, err := s.scimClient.Discovery().Schemas()
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Schemas should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Contains(s.T(), body, "totalResults")

	resourceTypes, _ := body["Resources"].([]interface{})
	require.GreaterOrEqual(s.T(), len(resourceTypes), 2, "Schemas should include at least User and Group")

	ids := map[string]bool{}
	for _, resource := range resourceTypes {
		schema, ok := resource.(map[string]interface{})
		if !ok {
			continue
		}
		id, ok := schema["id"].(string)
		if !ok {
			continue
		}
		ids[id] = true
	}
	require.True(s.T(), ids[scimclient.SCIMSchemaUser], "Schemas should include User schema")
	require.True(s.T(), ids[scimclient.SCIMSchemaGroup], "Schemas should include Group schema")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMSchemaByID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Schemas/{id} for User and Group schemas")

	for _, schemaID := range []string{scimclient.SCIMSchemaUser, scimclient.SCIMSchemaGroup} {
		resp, err := s.scimClient.Discovery().SchemaByID(schemaID)
		require.NoError(s.T(), err)
		require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, fmt.Sprintf("GET /Schemas/%s should return 200", schemaID)))

		var body map[string]interface{}
		require.NoError(s.T(), resp.DecodeJSON(&body))
		require.Equal(s.T(), schemaID, body["id"], "Schema id should match requested id")
		require.Contains(s.T(), body, "attributes", "Schema should have attributes field")
	}

	notFoundResp, err := s.scimClient.Discovery().SchemaByID("urn:nonexistent")
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(notFoundResp, http.StatusNotFound, "GET /Schemas/nonexistent should return 404"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUpdateGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PUT /Groups/{id}")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	newExternalID := "ext-" + groupName
	updateResp, err := s.scimClient.Groups().Update(groupID, scimclient.Group{
		Schemas:     []string{scimclient.SCIMSchemaGroup},
		ID:          groupID,
		DisplayName: groupName,
		ExternalID:  newExternalID,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(updateResp, http.StatusOK, "PUT /Groups/{id} should return 200"))

	body, err := scimactions.DecodeJSONBody(updateResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), groupID, body["id"])
	require.Equal(s.T(), groupName, body["displayName"])
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PUT")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUpdateUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PUT /Users/{id}")

	userName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	newExternalID := "ext-" + userName
	updateResp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   userName,
		ExternalID: newExternalID,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(updateResp, http.StatusOK, "PUT /Users/{id} should return 200"))

	body, err := scimactions.DecodeJSONBody(updateResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), userID, body["id"])
	require.Equal(s.T(), userName, body["userName"])
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PUT")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserProjectRoleBinding() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PRTB for SCIM user as project-owner")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err, "Should be able to create SCIM user")

	mgmtUser, err := s.client.Management.User.ByID(userID)
	require.NoError(s.T(), err, "Rancher user %s should exist after SCIM create", userID)

	project, err := projectapi.CreateProjectWithTemplate(s.client, s.cluster.ID, projectapi.NewProjectTemplate(s.cluster.ID))
	require.NoError(s.T(), err, "Should be able to create a project in cluster %s", s.cluster.ID)

	prtbNamespace := project.Status.BackingNamespace
	require.NoError(s.T(), authactions.WaitForNamespaceReady(s.client, prtbNamespace),
		"Project backing namespace %s should be ready before creating PRTB", prtbNamespace)

	projectName := fmt.Sprintf("%s:%s", project.Namespace, project.Name)

	prtbObj := &cattlev3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegen.AppendRandomString("prtb-"),
			Namespace: prtbNamespace,
		},
		ProjectName:      projectName,
		UserName:         mgmtUser.ID,
		RoleTemplateName: string(rbac.ProjectOwner),
	}

	prtb, err := extrbacapi.CreateProjectRoleTemplateBinding(s.client, prtbObj)
	require.NoError(s.T(), err, "Should be able to create PRTB for SCIM user %s", userID)
	require.NotEmpty(s.T(), prtb.Name)

	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(s.client, mgmtUser.ID, 1)
	require.NoError(s.T(), err, "Should find exactly 1 PRTB for SCIM user %s", userID)
	require.Equal(s.T(), projectName, prtbs[0].ProjectName, "PRTB should reference the correct project")
	require.Equal(s.T(), string(rbac.ProjectOwner), prtbs[0].RoleTemplateName, "PRTB role should be project-owner")
	require.Equal(s.T(), mgmtUser.ID, prtbs[0].UserName, "PRTB should be bound to the correct user")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMNoConfigMapAllEndpointsReturn404() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying all SCIM endpoints return 404 with SCIM error body when ConfigMap is absent")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.DeleteSCIMConfigMap(s.client, authactions.OpenLdap))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound))

	endpoints := []struct {
		name string
		call func() (*scimclient.Response, error)
	}{
		{"GET /Users", func() (*scimclient.Response, error) { return s.scimClient.Users().List(nil) }},
		{"GET /Groups", func() (*scimclient.Response, error) { return s.scimClient.Groups().List(nil) }},
		{"GET /ServiceProviderConfig", func() (*scimclient.Response, error) { return s.scimClient.Discovery().ServiceProviderConfig() }},
		{"GET /ResourceTypes", func() (*scimclient.Response, error) { return s.scimClient.Discovery().ResourceTypes() }},
		{"GET /Schemas", func() (*scimclient.Response, error) { return s.scimClient.Discovery().Schemas() }},
	}
	for _, ep := range endpoints {
		resp, err := ep.call()
		require.NoError(s.T(), err, "%s should not error", ep.name)
		require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusNotFound, fmt.Sprintf("%s should return 404 when ConfigMap absent", ep.name)))
		require.NoError(s.T(), scimactions.ValidateSCIMErrorBody(resp, http.StatusNotFound), "%s 404 body should match SCIM error schema", ep.name)
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMNoConfigMapRouteGatingBeforeAuth() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying invalid bearer token also returns 404 when ConfigMap absent (route gating before auth)")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.DeleteSCIMConfigMap(s.client, authactions.OpenLdap))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusNotFound))

	badClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, "invalid-token")
	resp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusNotFound, "Invalid token should still return 404 when ConfigMap absent (not 401)"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMEnabledTrueReturns200() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying enabled=true returns 200 on /Users after being disabled")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "false"}, http.StatusNotFound))
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap(), http.StatusOK))

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Users should return 200 with enabled=true"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMEnabledFalseReturns404() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying enabled=false returns 404 on /Users")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "false"}, http.StatusNotFound))

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusNotFound, "GET /Users should return 404 with enabled=false"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMPausedReturns503() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying paused=true returns 503 on /Users")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "paused": "true"}, http.StatusServiceUnavailable))

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusServiceUnavailable, "GET /Users should return 503 when paused"))
	require.NoError(s.T(), scimactions.ValidateSCIMErrorBody(resp, http.StatusServiceUnavailable))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMUnpauseResumes() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying unpause (paused=false) resumes SCIM endpoints")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "paused": "true"}, http.StatusServiceUnavailable))
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "paused": "false"}, http.StatusOK))

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Users should return 200 after unpause"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMUserIdExternalIDPrincipal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying userIdAttribute=externalId builds Rancher user principalID from externalId")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	externalID := namegen.AppendRandomString("ext")
	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalID, true)
	require.NoError(s.T(), err)

	mgmtUser, err := s.client.Management.User.ByID(userID)
	require.NoError(s.T(), err, "Should fetch Rancher user for SCIM user %s", userID)
	require.NotEmpty(s.T(), mgmtUser.PrincipalIDs, "Rancher user should have principalIDs")

	found := false
	for _, pid := range mgmtUser.PrincipalIDs {
		if strings.Contains(pid, externalID) {
			found = true
			break
		}
	}
	require.True(s.T(), found, "At least one principalID should contain externalId %q (got %v)", externalID, mgmtUser.PrincipalIDs)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMUserNameChangeAcceptedWithExternalIDPrincipal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH userName succeeds when userIdAttribute=externalId")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, namegen.AppendRandomString("ext"), true)
	require.NoError(s.T(), err)

	newUserName := namegen.AppendRandomString("renamed") + "@example.com"
	resp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "userName", newUserName)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "PATCH userName should return 200 when externalId is principal"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMUserNameChangeRejectedWithUserNamePrincipal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH userName returns 400 when userName is the principal (default)")

	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	resp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "userName", "newname@example.com")
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusBadRequest, "PATCH userName should return 400 when userName is principal"))
	require.NoError(s.T(), scimactions.ValidateSCIMErrorBody(resp, http.StatusBadRequest))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMGroupIdExternalIDPrincipal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying groupIdAttribute=externalId is accepted and group created with externalId")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "groupIdAttribute": "externalId"}))

	externalID := namegen.AppendRandomString("grp-ext")
	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, externalID)
	require.NoError(s.T(), err)

	getResp, err := s.scimClient.Groups().ByID(groupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Groups/{id} should return 200"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), externalID, body["externalId"], "externalId should be set on the group")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMDisplayNameFallsBackToUserName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying Rancher user.Name (displayName) falls back to userName when SCIM displayName is not set")

	userName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	mgmtUser, err := s.client.Management.User.ByID(userID)
	require.NoError(s.T(), err, "Should fetch Rancher user for SCIM user %s", userID)
	require.Equal(s.T(), userName, mgmtUser.Name, "Rancher user.Name should fall back to userName when SCIM displayName is not provided")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMPatchUserDisplayName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH replace displayName on user succeeds and is reflected in Rancher user record")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	newDisplayName := namegen.AppendRandomString("display")
	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "displayName", newDisplayName)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH displayName should return 200"))

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		mgmtUser, getErr := s.client.Management.User.ByID(userID)
		if getErr != nil {
			return false, nil
		}
		return mgmtUser.Name == newDisplayName, nil
	})
	require.NoError(s.T(), err, "Rancher user.Name should reflect SCIM displayName %q after PATCH", newDisplayName)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMFilterUserByExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users?filter=externalId eq returns matching user")

	externalID := namegen.AppendRandomString("ext-filter")
	_, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalID, true)
	require.NoError(s.T(), err)

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("externalId eq %q", externalID))

	resp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "Filtered GET /Users should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	userResources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), userResources, 1, "Filter by externalId should return exactly 1 user")

	firstUser, _ := userResources[0].(map[string]interface{})
	require.Equal(s.T(), externalID, firstUser["externalId"], "Returned user externalId should match filter")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMFilterGroupByExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Groups?filter=externalId eq returns matching group")

	externalID := namegen.AppendRandomString("grp-ext-filter")
	_, _, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, externalID)
	require.NoError(s.T(), err)

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("externalId eq %q", externalID))

	resp, err := s.scimClient.Groups().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "Filtered GET /Groups should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	groupResources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), groupResources, 1, "Filter by externalId should return exactly 1 group")

	firstGroup, _ := groupResources[0].(map[string]interface{})
	require.Equal(s.T(), externalID, firstGroup["externalId"], "Returned group externalId should match filter")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCMInvalidUserIdAttributeFallsBack() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying invalid userIdAttribute value falls back to default and does not crash SCIM")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "badvalue"}))

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), userID, "POST /Users should still succeed with invalid userIdAttribute (fallback to default)")

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Users should still return 200 with invalid userIdAttribute"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchUserActive() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH user with URN-prefixed active path behaves like bare path")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "urn:ietf:params:scim:schemas:core:2.0:User:active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH URN-prefixed active should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), false, body["active"], "active should be false after URN-prefixed PATCH")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchUserDisplayName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH user with URN-prefixed displayName path")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	newDisplayName := namegen.AppendRandomString("urn-display")
	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "urn:ietf:params:scim:schemas:core:2.0:User:displayName", newDisplayName)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH URN-prefixed displayName should return 200"))

	require.NoError(s.T(), scimactions.WaitForRancherUserName(s.client,userID, newDisplayName), "Rancher user.Name should reflect URN-prefixed displayName %q after PATCH", newDisplayName)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchUserName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH URN-prefixed userName succeeds when userIdAttribute=externalId")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, namegen.AppendRandomString("ext"), true)
	require.NoError(s.T(), err)

	newUserName := namegen.AppendRandomString("urn-rename") + "@example.com"
	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "urn:ietf:params:scim:schemas:core:2.0:User:userName", newUserName)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH URN-prefixed userName should return 200 with externalId principal"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchGroupExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH group with URN-prefixed externalId path")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	newExternalID := "ext-urn-" + groupName
	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "replace", "urn:ietf:params:scim:schemas:core:2.0:Group:externalId", newExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH URN-prefixed group externalId should return 200"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchGroupAddExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH group op:add with URN-prefixed externalId is treated as replace")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	newExternalID := "ext-urn-add-" + groupName
	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "urn:ietf:params:scim:schemas:core:2.0:Group:externalId", newExternalID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH op:add URN-prefixed group externalId should return 200 (treated as replace)"))

	body, err := scimactions.DecodeJSONBody(patchResp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PATCH op:add with URN prefix")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchGroupAddMember() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH group op:add with URN-prefixed members path")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)
	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "urn:ietf:params:scim:schemas:core:2.0:Group:members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH op:add URN-prefixed members should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNPatchGroupRemoveMember() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH group op:remove with URN-prefixed members[value eq ...] path")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)
	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	addResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(addResp, http.StatusOK, "PATCH add member should return 200"))

	removePath := fmt.Sprintf("urn:ietf:params:scim:schemas:core:2.0:Group:members[value eq %q]", userID)
	removeResp, err := scimactions.PatchGroup(s.scimClient, groupID, "remove", removePath, nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(removeResp, http.StatusOK, "PATCH op:remove URN-prefixed members should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 0))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNFilterUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users?filter with URN-prefixed attribute path")

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("urn:ietf:params:scim:schemas:core:2.0:User:userName eq %q", userName))

	resp, err := s.scimClient.Users().List(params)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "URN-prefixed filter on /Users should return 200"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	userResources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), userResources, 1, "URN-prefixed filter should return exactly 1 user")
	firstUser, _ := userResources[0].(map[string]interface{})
	require.Equal(s.T(), userName, firstUser["userName"], "Returned user userName should match filter")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNCrossResourceMismatchReturns400() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH group with User URN-prefixed path returns 400")

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "replace", "urn:ietf:params:scim:schemas:core:2.0:User:displayName", "wrong")
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusBadRequest, "User URN on Group endpoint should return 400"))
	require.NoError(s.T(), scimactions.ValidateSCIMErrorBody(patchResp, http.StatusBadRequest))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNUnknownURNReturns400() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH user with unknown URN path returns 400 and does not panic")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "urn:custom:extension:1.0:Custom:foo", "bar")
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusBadRequest, "Unknown URN should return 400"))
	require.NoError(s.T(), scimactions.ValidateSCIMErrorBody(patchResp, http.StatusBadRequest))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMURNBareAttributesRegression() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying bare-path PATCH still works after URN-stripping addition (regression)")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	userPatchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "active", true)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(userPatchResp, http.StatusOK, "Bare-path PATCH active should still return 200"))

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	groupPatchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "replace", "externalId", "ext-"+groupName)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(groupPatchResp, http.StatusOK, "Bare-path PATCH group externalId should still return 200"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLDisabledByDefault() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying rate limiting is disabled by default (no rate keys in ConfigMap)")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap(), http.StatusOK))

	results, err := scimactions.BurstSCIMRequests(s.scimClient, 50)
	require.NoError(s.T(), err)
	for i, code := range results {
		require.Equal(s.T(), http.StatusOK, code, "request %d should return 200 when rate limiting disabled", i)
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLEnabledReturns429AfterBurst() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying rate limit returns 429 when burst exceeded")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(2, 5)))

	results, err := scimactions.BurstSCIMRequests(s.scimClient, 20)
	require.NoError(s.T(), err)

	ok, throttled := scimactions.CountSCIMCodes(results)
	require.GreaterOrEqual(s.T(), ok, 1, "at least the burst capacity should return 200 (got ok=%d, 429=%d)", ok, throttled)
	require.GreaterOrEqual(s.T(), throttled, 1, "at least one request should be throttled with 429 (got ok=%d, 429=%d)", ok, throttled)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRL429ResponseFormat() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying 429 response format (Retry-After header, scim+json content-type, SCIM error body)")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1, 1)))

	throttledResp, err := scimactions.FindFirstThrottledResponse(s.scimClient, 10)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), throttledResp, "should observe at least one 429 within burst")

	require.Equal(s.T(), "application/scim+json", throttledResp.Header.Get("Content-Type"), "429 must use SCIM JSON content type")
	retryAfter, err := scimactions.GetRetryAfterSeconds(throttledResp)
	require.NoError(s.T(), err, "429 must include Retry-After header")
	require.Equal(s.T(), 1, retryAfter, "Retry-After should be 1 second")
	require.NoError(s.T(), scimactions.ValidateSCIMErrorBody(throttledResp, http.StatusTooManyRequests))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLTokenBucketRefillAfterRetryAfter() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying token bucket refills after Retry-After period elapses")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1, 1)))

	throttledResp, err := scimactions.FindFirstThrottledResponse(s.scimClient, 10)
	require.NoError(s.T(), err)
	require.NotNil(s.T(), throttledResp, "should observe a 429 with Retry-After")
	_, err = scimactions.GetRetryAfterSeconds(throttledResp)
	require.NoError(s.T(), err)

	var resp *scimclient.Response
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveHundredMillisecondTimeout, defaults.OneMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		r, listErr := s.scimClient.Users().List(nil)
		if listErr != nil {
			return false, nil
		}
		resp = r
		return r.StatusCode == http.StatusOK, nil
	})
	require.NoError(s.T(), err, "request after Retry-After window should eventually return 200")
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "request after Retry-After wait should return 200"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLAppliesToAllMethods() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying rate limit applies to PATCH (non-GET method)")

	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))
	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1, 1)))

	var ok, throttled int
	for i := 0; i < 10; i++ {
		resp, patchErr := scimactions.PatchUser(s.scimClient, userID, "replace", "active", i%2 == 0)
		require.NoError(s.T(), patchErr)
		switch resp.StatusCode {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			throttled++
		}
	}
	require.GreaterOrEqual(s.T(), throttled, 1, "rate limit should also apply to PATCH (got ok=%d, 429=%d)", ok, throttled)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLDynamicConfigChange() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying ConfigMap rate-limit change takes effect on next request without restart")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1, 1)))

	throttled, err := scimactions.VerifySCIMThrottle(s.scimClient, 10)
	require.NoError(s.T(), err)
	require.True(s.T(), throttled, "rate limit should engage at low rps before reconfig")

	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1000, 1000)))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK), "endpoint should serve 200 after raising rate limit (token bucket refill)")

	results, err := scimactions.BurstSCIMRequests(s.scimClient, 20)
	require.NoError(s.T(), err)
	ok, _ := scimactions.CountSCIMCodes(results)
	require.GreaterOrEqual(s.T(), ok, len(results)*8/10, "after raising rate limit to 1000 rps / burst 1000, the vast majority of a 20-request burst should return 200 (got ok=%d/%d)", ok, len(results))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLInvalidValuesFallback() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying invalid rate-limit values fall back to defaults and do not crash")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{
		"enabled":                    "true",
		"rateLimitRequestsPerSecond": "not-a-number",
		"rateLimitBurst":             "-5",
	}, http.StatusOK))

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "SCIM should still serve requests with invalid rate-limit values"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRLReDisableRateLimit() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying rateLimitRequestsPerSecond=0 re-disables rate limiting")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(1, 1)))

	throttled, err := scimactions.VerifySCIMThrottle(s.scimClient, 10)
	require.NoError(s.T(), err)
	require.True(s.T(), throttled, "rate limit should engage with low rps")

	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.RateLimitSCIMConfigMap(0, 0)))

	results, err := scimactions.BurstSCIMRequests(s.scimClient, 50)
	require.NoError(s.T(), err)
	for i, code := range results {
		require.Equal(s.T(), http.StatusOK, code, "after re-disabling rate limit, request %d should return 200", i)
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGroupMemberMatchingUsesPrincipalName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying group PATCH add member resolves via principal Name (not DisplayName)")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)
	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH add member by principal id should return 200"))
	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))

	getResp, err := s.scimClient.Groups().ByID(groupID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(getResp, http.StatusOK, "GET /Groups/{id} should return 200"))

	body, err := scimactions.DecodeJSONBody(getResp)
	require.NoError(s.T(), err)
	members, _ := body["members"].([]interface{})
	require.Len(s.T(), members, 1, "Group should have exactly 1 member after PATCH add")
	member, _ := members[0].(map[string]interface{})
	require.Equal(s.T(), userID, member["value"], "Member value should resolve to the user principal id")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMZRegTokenRotation() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM token rotation: old token rejected, new token works")

	oldToken, err := scimactions.FetchSCIMBearerToken(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Should fetch existing SCIM token")
	require.NotEmpty(s.T(), oldToken)

	clusterContext, err := s.client.WranglerContext.Core.Secret().List(scimactions.SCIMSecretNamespace, metav1.ListOptions{
		LabelSelector: fmt.Sprintf("cattle.io/kind=scim-auth-token,authn.management.cattle.io/provider=%s", authactions.OpenLdap),
	})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), clusterContext.Items, "Should find SCIM token secret")

	for i := range clusterContext.Items {
		err = s.client.WranglerContext.Core.Secret().Delete(scimactions.SCIMSecretNamespace, clusterContext.Items[i].Name, &metav1.DeleteOptions{})
		require.NoError(s.T(), err, "Should delete old SCIM token secret")
	}

	newToken, err := scimactions.CreateSCIMTokenSecret(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Should create a new SCIM token")
	require.NotEqual(s.T(), oldToken, newToken, "New token must differ from old")

	defer func() {
		s.scimClient = scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, newToken)
	}()

	oldClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, oldToken)
	oldResp, err := oldClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(oldResp, http.StatusUnauthorized, "old SCIM token should be rejected with 401"))

	newClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, newToken)
	newResp, err := newClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(newResp, http.StatusOK, "new SCIM token should be accepted with 200"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMRegConfigMapKeyParsingNonInterference() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying rate-limit ConfigMap keys do not interfere with parsing of enabled/paused/userIdAttribute")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap, map[string]string{
		"enabled":                    "true",
		"paused":                     "false",
		"userIdAttribute":            "externalId",
		"rateLimitRequestsPerSecond": "1000",
		"rateLimitBurst":             "1000",
	}, http.StatusOK))

	externalID := namegen.AppendRandomString("ext-noninterference")
	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalID, true)
	require.NoError(s.T(), err)

	mgmtUser, err := s.client.Management.User.ByID(userID)
	require.NoError(s.T(), err)
	found := false
	for _, pid := range mgmtUser.PrincipalIDs {
		if strings.Contains(pid, externalID) {
			found = true
			break
		}
	}
	require.True(s.T(), found, "userIdAttribute=externalId should still apply when rate-limit keys are also set")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPostReprovisionsDisabledUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying POST with externalId matching a disabled user re-provisions and returns 200")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	externalID := namegen.AppendRandomString("ext-reprovision")
	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalID, true)
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH active=false should return 200"))

	resp, err := s.scimClient.Users().Create(scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   namegen.AppendRandomString("reprov-user"),
		ExternalID: externalID,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "POST with externalId of disabled user should return 200 (re-provision)"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), userID, body["id"], "re-provisioned user should have the same id as the original (not a duplicate)")
	require.Equal(s.T(), true, body["active"], "re-provisioned user should be active")
	require.Equal(s.T(), externalID, body["externalId"], "externalId should match")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPostDuplicateActiveExternalIDReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying POST with externalId matching an active user returns 409")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	externalID := namegen.AppendRandomString("ext-dup-active")
	_, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalID, true)
	require.NoError(s.T(), err)

	resp, err := s.scimClient.Users().Create(scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   namegen.AppendRandomString("dup-user"),
		ExternalID: externalID,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusConflict, "POST with externalId of active user should return 409"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPutUserNameImmutableReturns400() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PUT with changed userName returns 400 with scimType mutability")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	resp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: namegen.AppendRandomString("renamed-put"),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusBadRequest, "PUT with changed userName should return 400"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "mutability", body["scimType"], "error body should have scimType=mutability")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchUserNameMutabilityError() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH op:replace userName returns 400 with scimType mutability when userName is principal")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", false)
	require.NoError(s.T(), err)

	resp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "userName", namegen.AppendRandomString("renamed-patch"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusBadRequest, "PATCH op:replace userName should return 400"))

	body, err := scimactions.DecodeJSONBody(resp)
	require.NoError(s.T(), err)
	require.Equal(s.T(), "mutability", body["scimType"], "error body should have scimType=mutability")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchDeactivateAdminReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH active=false on default admin returns 409")

	adminUsers, err := s.client.Management.User.List(&types.ListOpts{
		Filters: map[string]interface{}{"username": "admin"},
	})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), adminUsers.Data, "default admin user should exist")

	resp, err := scimactions.PatchUser(s.scimClient, adminUsers.Data[0].ID, "replace", "active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusConflict, "PATCH active=false on default admin should return 409"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchExternalIDConflictReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH externalId to a value already held by another user returns 409 (userName mode, where externalId is mutable)")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))

	externalIDA := namegen.AppendRandomString("ext-conflict-a")
	_, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalIDA, true)
	require.NoError(s.T(), err)

	_, userIDB, err := scimactions.CreateSCIMUser(s.scimClient, subSession, namegen.AppendRandomString("ext-conflict-b"), true)
	require.NoError(s.T(), err)

	resp, err := scimactions.PatchUser(s.scimClient, userIDB, "replace", "externalId", externalIDA)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusConflict, "PATCH externalId to another user's value should return 409"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPutUserNameConflictReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PUT userName to a value already held by another user returns 409")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	userNameA, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, namegen.AppendRandomString("un-conflict-a-ext"), true)
	require.NoError(s.T(), err)

	externalIDB := namegen.AppendRandomString("un-conflict-b-ext")
	_, userIDB, err := scimactions.CreateSCIMUser(s.scimClient, subSession, externalIDB, true)
	require.NoError(s.T(), err)

	resp, err := s.scimClient.Users().Update(userIDB, scimclient.User{
		Schemas:    []string{scimclient.SCIMSchemaUser},
		UserName:   userNameA,
		ExternalID: externalIDB,
		Active:     scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusConflict, "PUT userName to another user's value should return 409"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPrincipalSearchUserNotLocal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a SCIM-provisioned user is not returned as a local principal by /v3/principals search")

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	require.NoError(s.T(), authactions.VerifyPrincipalNotLocal(s.client, userName))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPrincipalSearchGroupNotLocal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a SCIM-provisioned group is not returned as a local principal by /v3/principals search")

	groupName, _, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	require.NoError(s.T(), authactions.VerifyPrincipalNotLocal(s.client, groupName))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGetUserMissingUserAttributeReturns200() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users/{id} returns 200 for a SCIM user whose UserAttribute is absent (never logged in)")

	_, userID, err := scimactions.ProvisionSCIMUserWithoutAttribute(s.client, s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	resp, err := s.scimClient.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Users/{id} should return 200 even when the user has no UserAttribute"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPutUserMissingUserAttributeReturns200() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PUT /Users/{id} returns 200 for a SCIM user whose UserAttribute is absent (externalId mode, userName mutable)")

	defer func() { require.NoError(s.T(), scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)) }()
	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWaitCreateReady(s.client, s.scimClient, authactions.OpenLdap, map[string]string{"enabled": "true", "userIdAttribute": "externalId"}))

	externalID := namegen.AppendRandomString("ext-put-recover")
	_, userID, err := scimactions.ProvisionSCIMUserWithoutAttribute(s.client, s.scimClient, subSession, externalID)
	require.NoError(s.T(), err)

	resp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: namegen.AppendRandomString("put-recovered"),
		Active:   scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "PUT /Users/{id} should return 200 even when the user has no UserAttribute"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchUserMissingUserAttributeReturns200() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH /Users/{id} returns 200 for a SCIM user whose UserAttribute is absent")

	_, userID, err := scimactions.ProvisionSCIMUserWithoutAttribute(s.client, s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	resp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "displayName", namegen.AppendRandomString("patched"))
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "PATCH /Users/{id} should return 200 even when the user has no UserAttribute"))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGroupAddMemberMissingUserAttribute() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH group add member records membership when the member's UserAttribute is absent")

	_, userID, err := scimactions.ProvisionSCIMUserWithoutAttribute(s.client, s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	_, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	patchResp, err := scimactions.PatchGroup(s.scimClient, groupID, "add", "members", []scimclient.Member{{Value: userID}})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH group add member should return 200 when the member has no UserAttribute"))

	require.NoError(s.T(), scimactions.WaitForGroupMemberCount(s.scimClient, groupID, 1))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPrincipalSearchLocalUserIsLocal() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a genuine local user is still returned as a local principal by /v3/principals search")

	adminUsers, err := s.client.Management.User.List(&types.ListOpts{
		Filters: map[string]interface{}{"username": "admin"},
	})
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), adminUsers.Data, "default admin user should exist")

	require.NoError(s.T(), authactions.VerifyPrincipalIsLocal(s.client, adminUsers.Data[0].Username))
}

func TestSCIMOpenLDAPSuite(t *testing.T) {
	suite.Run(t, new(SCIMOpenLDAPTestSuite))
}

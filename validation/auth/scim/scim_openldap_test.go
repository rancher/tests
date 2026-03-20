//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package scim

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v3 "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"

	cattlev3 "github.com/rancher/rancher/pkg/apis/management.cattle.io/v3"
	authactions "github.com/rancher/tests/actions/auth"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	rbacapi "github.com/rancher/tests/actions/kubeapi/rbac"
	scimactions "github.com/rancher/tests/actions/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const scimProvider = authactions.OpenLdap

type SCIMOpenLDAPTestSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	cluster    *v3.Cluster
	adminUser  *v3.User
	authConfig *authactions.AuthConfig
	scim       *scimactions.Client
}

func (s *SCIMOpenLDAPTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Loading OpenLDAP auth configuration from config file")
	s.authConfig = new(authactions.AuthConfig)
	config.LoadConfig(authactions.OpenLdapAuthInput, s.authConfig)
	require.NotNil(s.T(), s.authConfig, "Auth configuration is not provided")

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(s.T(), clusterName, "Cluster name should be set")

	clusterID, err := clusters.GetClusterIDByName(s.client, clusterName)
	require.NoError(s.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	s.cluster, err = s.client.Management.Cluster.ByID(clusterID)
	require.NoError(s.T(), err, "Failed to retrieve cluster by ID: %s", clusterID)

	logrus.Info("Setting up admin user credentials for OpenLDAP authentication")
	s.adminUser = &v3.User{
		Username: client.Auth.OLDAP.Config.Users.Admin.Username,
		Password: client.Auth.OLDAP.Config.Users.Admin.Password,
	}

	logrus.Info("Setting up SCIM client for OpenLDAP provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, scimProvider)
	require.NoError(s.T(), err, "Failed to setup SCIM client")
	s.scim = scimClient
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
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMFeatureFlagEnabled() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying SCIM feature flag is enabled")

	enabled, err := scimactions.IsSCIMFeatureFlagEnabled(s.client)
	require.NoError(s.T(), err, "Failed to check SCIM feature flag state")
	require.True(s.T(), enabled, "SCIM feature flag should be enabled")

	resp, err := s.scim.Discovery().ServiceProviderConfig()
	require.NoError(s.T(), err, "GET /ServiceProviderConfig should not error")
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "ServiceProviderConfig should return 200, body: %s", string(resp.Body))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMTokenSecretExistsInKubeAPI() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying SCIM token secret exists in %s via label selector for provider %s", scimactions.SCIMSecretNamespace, scimProvider)

	token, err := scimactions.FetchSCIMBearerToken(s.client, scimProvider)
	require.NoError(s.T(), err, "SCIM token secret should exist in %s", scimactions.SCIMSecretNamespace)
	require.NotEmpty(s.T(), token, "SCIM bearer token should not be empty")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMZFeatureFlagDisableAndReenableEndpoint() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Disabling SCIM flag and verifying /Users returns 404")

	err := scimactions.DisableSCIMFeatureFlag(s.client)
	require.NoError(s.T(), err, "Should be able to disable SCIM feature flag")

	resp, err := s.scim.Users().List(nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode, "GET /Users should return 404 when SCIM is disabled, body: %s", string(resp.Body))

	logrus.Info("Re-enabling SCIM flag after test")
	err = scimactions.ReenableSCIMFeatureFlag(s.client, scimProvider)
	require.NoError(s.T(), err, "Should be able to re-enable SCIM feature flag")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMServiceProviderConfig() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /ServiceProviderConfig response")

	resp, err := s.scim.Discovery().ServiceProviderConfig()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "ServiceProviderConfig should return 200, body: %s", string(resp.Body))

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

	resp, err := s.scim.Discovery().ResourceTypes()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "ResourceTypes should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
	require.NoError(s.T(), err)

	resources, ok := body["Resources"].([]interface{})
	require.True(s.T(), ok, "ResourceTypes response should have Resources array")
	require.GreaterOrEqual(s.T(), len(resources), 2, "Should have at least User and Group resource types")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMInvalidTokenReturns401() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying invalid bearer token returns 401")

	badClient := scimactions.NewSCIMClientWithToken(
		fmt.Sprintf("https://%s", s.client.RancherConfig.Host),
		scimProvider,
		"invalid-token",
	)

	resp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusUnauthorized, resp.StatusCode, "Invalid token should return 401, body: %s", string(resp.Body))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMListUsers() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users returns 200 with valid ListResponse")

	resp, err := s.scim.Users().List(nil)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "GET /Users should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
	require.NoError(s.T(), err)
	require.Contains(s.T(), body, "totalResults", "ListResponse should have totalResults")
	require.Contains(s.T(), body, "Resources", "ListResponse should have Resources array")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateAndGetUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Creating SCIM user %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:    []string{scimactions.SCIMSchemaUser},
		UserName:   userName,
		ExternalID: "ext-" + userName,
		Active:     scimactions.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode, "POST /Users should return 201, body: %s", string(createResp.Body))

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err, "Created user response should contain an id")

	defer func() {
		logrus.Infof("Deleting SCIM user %s", userID)
		_, _ = s.scim.Users().Delete(userID)
	}()

	getResp, err := s.scim.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, getResp.StatusCode, "GET /Users/{id} should return 200, body: %s", string(getResp.Body))

	var body map[string]interface{}
	err = getResp.DecodeJSON(&body)
	require.NoError(s.T(), err)
	require.Equal(s.T(), userName, body["userName"], "userName in response should match created value")
	require.Equal(s.T(), userID, body["id"], "id in response should match")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateDuplicateUserReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying duplicate userName returns 409 for %s", userName)

	user := scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
		Active:   scimactions.BoolPtr(true),
	}

	resp1, err := s.scim.Users().Create(user)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, resp1.StatusCode, "First POST /Users should return 201, body: %s", string(resp1.Body))

	userID, err := resp1.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Users().Delete(userID)
	}()

	resp2, err := s.scim.Users().Create(user)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusConflict, resp2.StatusCode, "Duplicate POST /Users should return 409, body: %s", string(resp2.Body))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMFilterUserByUserName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying filter by userName for %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
		Active:   scimactions.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Users().Delete(userID)
	}()

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("userName eq %q", userName))

	filterResp, err := s.scim.Users().List(params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, filterResp.StatusCode, "Filtered GET /Users should return 200, body: %s", string(filterResp.Body))

	var body map[string]interface{}
	err = filterResp.DecodeJSON(&body)
	require.NoError(s.T(), err)

	resources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), resources, 1, "Filter should return exactly 1 user")

	firstUser, _ := resources[0].(map[string]interface{})
	require.Equal(s.T(), userName, firstUser["userName"], "Returned user userName should match filter")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchDeactivateUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying PATCH deactivate for user %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode, "POST /Users should return 201, body: %s", string(createResp.Body))

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Users().Delete(userID)
	}()

	getResp, err := s.scim.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, getResp.StatusCode, "GET /Users/{id} should return 200 before PATCH, body: %s", string(getResp.Body))

	patchResp, err := s.scim.Users().Patch(userID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "active", Value: false},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH active=false should return 200, body: %s", string(patchResp.Body))

	var body map[string]interface{}
	err = patchResp.DecodeJSON(&body)
	require.NoError(s.T(), err)
	require.Equal(s.T(), false, body["active"], "active should be false after deactivation")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchReactivateUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying PATCH reactivate for user %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode, "POST /Users should return 201, body: %s", string(createResp.Body))

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Users().Delete(userID)
	}()

	deactivateResp, err := s.scim.Users().Patch(userID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "active", Value: false},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, deactivateResp.StatusCode, "PATCH active=false should return 200, body: %s", string(deactivateResp.Body))

	patchResp, err := s.scim.Users().Patch(userID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "active", Value: true},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH active=true should return 200, body: %s", string(patchResp.Body))

	var body map[string]interface{}
	err = patchResp.DecodeJSON(&body)
	require.NoError(s.T(), err)
	require.Equal(s.T(), true, body["active"], "active should be true after reactivation")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMDeleteUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying DELETE /Users/{id} for %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
		Active:   scimactions.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)

	deleteResp, err := s.scim.Users().Delete(userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNoContent, deleteResp.StatusCode, "DELETE /Users/{id} should return 204, body: %s", string(deleteResp.Body))

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		getResp, pollErr := s.scim.Users().ByID(userID)
		if pollErr != nil {
			return false, pollErr
		}
		return getResp.StatusCode == http.StatusNotFound, nil
	})
	require.NoError(s.T(), err, "User %s should return 404 after DELETE", userID)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGetNonExistentUserReturns404() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Users/nonexistent-id returns 404")

	resp, err := s.scim.Users().ByID("nonexistent-id-99999")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNotFound, resp.StatusCode, "GET non-existent user should return 404, body: %s", string(resp.Body))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserPagination() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying /Users startIndex and count pagination parameters")

	params := url.Values{}
	params.Set("startIndex", "1")
	params.Set("count", "2")

	resp, err := s.scim.Users().List(params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "Paginated GET /Users should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
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

	resp, err := s.scim.Users().List(params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "Out-of-bounds startIndex should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
	require.NoError(s.T(), err)

	resources, _ := body["Resources"].([]interface{})
	require.Empty(s.T(), resources, "Resources should be empty for out-of-bounds startIndex")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateAndGetGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Creating SCIM group %s", groupName)

	createResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode, "POST /Groups should return 201, body: %s", string(createResp.Body))

	groupID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		logrus.Infof("Deleting SCIM group %s", groupID)
		_, _ = s.scim.Groups().Delete(groupID)
	}()

	getResp, err := s.scim.Groups().ByID(groupID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, getResp.StatusCode, "GET /Groups/{id} should return 200, body: %s", string(getResp.Body))

	var body map[string]interface{}
	err = getResp.DecodeJSON(&body)
	require.NoError(s.T(), err)
	require.Equal(s.T(), groupName, body["displayName"], "displayName should match")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCreateDuplicateGroupReturns409() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying duplicate group displayName returns 409 for %s", groupName)

	group := scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	}

	resp1, err := s.scim.Groups().Create(group)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, resp1.StatusCode)

	groupID, err := resp1.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Groups().Delete(groupID)
	}()

	resp2, err := s.scim.Groups().Create(group)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusConflict, resp2.StatusCode, "Duplicate POST /Groups should return 409, body: %s", string(resp2.Body))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchAddMemberToGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying PATCH add member: group=%s user=%s", groupName, userName)

	userResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
		Active:   scimactions.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, userResp.StatusCode)

	userID, err := userResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Users().Delete(userID)
	}()

	groupResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, groupResp.StatusCode)

	groupID, err := groupResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Groups().Delete(groupID)
	}()

	patchResp, err := s.scim.Groups().Patch(groupID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "add", Path: "members", Value: []scimactions.Member{{Value: userID}}},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH add member should return 200, body: %s", string(patchResp.Body))

	var memberCount int
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		getResp, err := s.scim.Groups().ByID(groupID)
		if err != nil || getResp.StatusCode != http.StatusOK {
			return false, nil
		}
		var body map[string]interface{}
		if err := getResp.DecodeJSON(&body); err != nil {
			return false, nil
		}
		members, _ := body["members"].([]interface{})
		memberCount = len(members)
		return memberCount == 1, nil
	})
	require.NoError(s.T(), err, "Timed out waiting for group member to appear via GET")
	require.Equal(s.T(), 1, memberCount, "Group should have exactly 1 member after PATCH add")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchRemoveMemberFromGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying PATCH remove member: group=%s user=%s", groupName, userName)

	userResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
		Active:   scimactions.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, userResp.StatusCode)

	userID, err := userResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Users().Delete(userID)
	}()

	groupResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, groupResp.StatusCode)

	groupID, err := groupResp.IDFromBody()
	require.NoError(s.T(), err)

	defer func() {
		_, _ = s.scim.Groups().Delete(groupID)
	}()

	addResp, err := s.scim.Groups().Patch(groupID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "add", Path: "members", Value: []scimactions.Member{{Value: userID}}},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, addResp.StatusCode, "PATCH add member should return 200, body: %s", string(addResp.Body))

	patchResp, err := s.scim.Groups().Patch(groupID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "remove", Path: fmt.Sprintf("members[value eq %q]", userID)},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH remove member should return 200, body: %s", string(patchResp.Body))

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		getResp, err := s.scim.Groups().ByID(groupID)
		if err != nil || getResp.StatusCode != http.StatusOK {
			return false, nil
		}
		var body map[string]interface{}
		if err := getResp.DecodeJSON(&body); err != nil {
			return false, nil
		}
		members, _ := body["members"].([]interface{})
		return len(members) == 0, nil
	})
	require.NoError(s.T(), err, "Timed out waiting for group member to be removed via GET")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGroupListExcludeMembersAttribute() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Groups?excludedAttributes=members omits members field")

	params := url.Values{}
	params.Set("excludedAttributes", "members")

	resp, err := s.scim.Groups().List(params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "GET /Groups?excludedAttributes=members should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
	require.NoError(s.T(), err)

	resources, _ := body["Resources"].([]interface{})
	for _, r := range resources {
		grp, _ := r.(map[string]interface{})
		_, hasMembersField := grp["members"]
		require.False(s.T(), hasMembersField, "members field should be absent when excludedAttributes=members")
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCannotDeleteDefaultAdmin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying default admin user is not visible via SCIM (local users are not SCIM-provisioned)")

	params := url.Values{}
	params.Set("filter", `userName eq "admin"`)

	resp, err := s.scim.Users().List(params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "Filter request should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	err = resp.DecodeJSON(&body)
	require.NoError(s.T(), err)

	resources, _ := body["Resources"].([]interface{})
	require.Empty(s.T(), resources, "Local admin should not appear in SCIM — only SCIM-provisioned users are returned")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMCannotDeactivateDefaultAdmin() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying PATCH active=false on non-existent SCIM user returns 404")

	patchResp, err := s.scim.Users().Patch("nonexistent-admin-id", scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "active", Value: false},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNotFound, patchResp.StatusCode,
		"PATCH on non-existent user should return 404, body: %s", string(patchResp.Body))
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMOpenLDAPAuthUnaffectedBySCIM() {
	subSession, authAdmin, err := authactions.SetupAuthenticatedSession(s.client, s.session, s.adminUser, authactions.OpenLdap)
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

	token, err := scimactions.FetchSCIMBearerToken(s.client, scimProvider)
	require.NoError(s.T(), err, "SCIM secret should still be present after auth regression tests")
	require.NotEmpty(s.T(), token, "SCIM bearer token should still be non-empty")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMAuthConfigEnabledViaSteve() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying OpenLDAP authconfig reports enabled=true via Steve")

	authConfigResource, err := s.client.Steve.SteveType("management.cattle.io.authconfig").ByID(authactions.OpenLdap)
	require.NoError(s.T(), err, "Should be able to GET openldap authconfig via Steve")
	require.NotNil(s.T(), authConfigResource)

	enabled, _ := authConfigResource.JSONResp["enabled"].(bool)
	require.True(s.T(), enabled, "OpenLDAP authconfig should report enabled=true via Steve")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMAuthUsersAsClusterMembers() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	require.NotEmpty(s.T(), s.authConfig.Users, "At least one auth user must be configured in cattle-config.yaml")

	for _, authUser := range s.authConfig.Users {
		logrus.Infof("Creating cluster-member CRTB for auth user %s", authUser.Username)

		userV3 := &v3.User{Username: authUser.Username, Password: authUser.Password}
		authAdmin, err := authactions.LoginAsAuthUser(s.client, userV3, scimProvider)
		require.NoError(s.T(), err, "Auth user %s should be able to login", authUser.Username)

		mgmtUser, err := s.client.Management.User.ByID(authAdmin.UserID)
		require.NoError(s.T(), err, "Should fetch Rancher user for auth user %s", authUser.Username)

		crtbObj := &cattlev3.ClusterRoleTemplateBinding{
			ObjectMeta:       metav1.ObjectMeta{Namespace: s.cluster.ID, GenerateName: "crtb-"},
			ClusterName:      s.cluster.ID,
			UserName:         mgmtUser.ID,
			RoleTemplateName: "cluster-member",
		}
		crtb, err := s.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Create(crtbObj)
		require.NoError(s.T(), err, "Should be able to create CRTB for auth user %s", authUser.Username)
		require.NoError(s.T(), rbacapi.WaitForCrtbStatus(s.client, crtb.Namespace, crtb.Name))

		crtbs, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUser.ID, 1)
		require.NoError(s.T(), err, "Should find exactly 1 CRTB for auth user %s", authUser.Username)
		require.Equal(s.T(), s.cluster.ID, crtbs[0].ClusterName, "CRTB should be for the correct cluster")
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGroupRoleBindings() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying group CRTB for SCIM group %s", groupName)

	groupResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, groupResp.StatusCode)

	groupID, err := groupResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Groups().Delete(groupID) }()

	var userIDs []string
	for i := 0; i < 2; i++ {
		userName := namegen.AppendRandomString("scim-user")
		uResp, err := s.scim.Users().Create(scimactions.User{
			Schemas:  []string{scimactions.SCIMSchemaUser},
			UserName: userName,
		})
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusCreated, uResp.StatusCode)

		uid, err := uResp.IDFromBody()
		require.NoError(s.T(), err)
		userIDs = append(userIDs, uid)
		defer func(id string) { _, _ = s.scim.Users().Delete(id) }(uid)

		addResp, err := s.scim.Groups().Patch(groupID, scimactions.PatchOp{
			Schemas: []string{scimactions.SCIMSchemaPatchOp},
			Operations: []scimactions.Operation{
				{Op: "add", Path: "members", Value: []scimactions.Member{{Value: uid}}},
			},
		})
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusOK, addResp.StatusCode)
	}

	groupPrincipal := fmt.Sprintf("%s_group://%s", scimProvider, groupName)
	logrus.Infof("Creating group CRTB for principal %s", groupPrincipal)

	crtb, err := rbacapi.CreateGroupClusterRoleTemplateBinding(s.client, s.cluster.ID, groupPrincipal, "cluster-member")
	require.NoError(s.T(), err, "Should be able to create group CRTB")
	require.NotEmpty(s.T(), crtb.Name)

	foundCRTB, err := rbacapi.GetClusterRoleTemplateBindingsForGroup(s.client, groupPrincipal, s.cluster.ID)
	require.NoError(s.T(), err, "Should find CRTB for group %s", groupPrincipal)
	require.Equal(s.T(), groupPrincipal, foundCRTB.GroupPrincipalName)
	require.Equal(s.T(), s.cluster.ID, foundCRTB.ClusterName)
	require.Equal(s.T(), "cluster-member", foundCRTB.RoleTemplateName)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserPaginationMultiPage() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying multi-page pagination: creating 12 SCIM users")

	const numUsers = 12
	var createdIDs []string
	for i := 0; i < numUsers; i++ {
		resp, err := s.scim.Users().Create(scimactions.User{
			Schemas:  []string{scimactions.SCIMSchemaUser},
			UserName: namegen.AppendRandomString("scim-page-user"),
		})
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusCreated, resp.StatusCode)

		uid, err := resp.IDFromBody()
		require.NoError(s.T(), err)
		createdIDs = append(createdIDs, uid)
	}
	defer func() {
		for _, id := range createdIDs {
			_, _ = s.scim.Users().Delete(id)
		}
	}()

	p1 := url.Values{}
	p1.Set("startIndex", "1")
	p1.Set("count", "5")
	r1, err := s.scim.Users().List(p1)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, r1.StatusCode)

	var b1 map[string]interface{}
	require.NoError(s.T(), r1.DecodeJSON(&b1))
	totalResults := int(b1["totalResults"].(float64))
	require.GreaterOrEqual(s.T(), totalResults, numUsers, "totalResults should be at least the number of users we created")
	require.Equal(s.T(), float64(1), b1["startIndex"], "startIndex should be 1")
	require.Equal(s.T(), float64(5), b1["itemsPerPage"], "page 1 should have 5 items")
	resources1, _ := b1["Resources"].([]interface{})
	require.Len(s.T(), resources1, 5, "page 1 should return exactly 5 users")

	p2 := url.Values{}
	p2.Set("startIndex", "6")
	p2.Set("count", "5")
	r2, err := s.scim.Users().List(p2)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, r2.StatusCode)

	var b2 map[string]interface{}
	require.NoError(s.T(), r2.DecodeJSON(&b2))
	require.Equal(s.T(), float64(totalResults), b2["totalResults"], "totalResults should be consistent across pages")
	require.Equal(s.T(), float64(6), b2["startIndex"])
	require.Equal(s.T(), float64(5), b2["itemsPerPage"])
	resources2, _ := b2["Resources"].([]interface{})
	require.Len(s.T(), resources2, 5, "page 2 should return exactly 5 users")

	p3 := url.Values{}
	p3.Set("startIndex", "11")
	p3.Set("count", fmt.Sprintf("%d", totalResults))
	r3, err := s.scim.Users().List(p3)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, r3.StatusCode)

	var b3 map[string]interface{}
	require.NoError(s.T(), r3.DecodeJSON(&b3))
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

	userNameA := namegen.AppendRandomString("scim-user")
	logrus.Infof("Creating SCIM user %s with cluster-member CRTB", userNameA)

	respA, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userNameA,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, respA.StatusCode)

	userIDA, err := respA.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Users().Delete(userIDA) }()

	mgmtUserA, err := s.client.Management.User.ByID(userIDA)
	require.NoError(s.T(), err, "Should be able to fetch Rancher user for SCIM user %s", userNameA)

	crtbObjA := &cattlev3.ClusterRoleTemplateBinding{
		ObjectMeta:       metav1.ObjectMeta{Namespace: s.cluster.ID, GenerateName: "crtb-"},
		ClusterName:      s.cluster.ID,
		UserName:         mgmtUserA.ID,
		RoleTemplateName: "cluster-member",
	}
	crtb, err := s.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Create(crtbObjA)
	require.NoError(s.T(), err, "Should be able to create CRTB for SCIM user %s", userNameA)
	require.NotEmpty(s.T(), crtb.Name)
	require.NoError(s.T(), rbacapi.WaitForCrtbStatus(s.client, crtb.Namespace, crtb.Name))

	crtbsA, err := rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUserA.ID, 1)
	require.NoError(s.T(), err, "User A should have exactly 1 CRTB")
	require.Equal(s.T(), s.cluster.ID, crtbsA[0].ClusterName)
	require.Equal(s.T(), "cluster-member", crtbsA[0].RoleTemplateName)

	userNameB := namegen.AppendRandomString("scim-user")
	logrus.Infof("Creating SCIM user %s without any CRTB", userNameB)

	respB, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userNameB,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, respB.StatusCode)

	userIDB, err := respB.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Users().Delete(userIDB) }()

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
		userName := namegen.AppendRandomString("scim-cluster-user")
		logrus.Infof("Creating SCIM cluster member %s", userName)

		createResp, err := s.scim.Users().Create(scimactions.User{
			Schemas:  []string{scimactions.SCIMSchemaUser},
			UserName: userName,
		})
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

		userID, err := createResp.IDFromBody()
		require.NoError(s.T(), err)
		defer func(id string) { _, _ = s.scim.Users().Delete(id) }(userID)

		mgmtUser, err := s.client.Management.User.ByID(userID)
		require.NoError(s.T(), err, "Rancher user %s should exist after SCIM create", userID)

		crtbObj := &cattlev3.ClusterRoleTemplateBinding{
			ObjectMeta:       metav1.ObjectMeta{Namespace: s.cluster.ID, GenerateName: "crtb-"},
			ClusterName:      s.cluster.ID,
			UserName:         mgmtUser.ID,
			RoleTemplateName: "cluster-member",
		}
		crtb, err := s.client.WranglerContext.Mgmt.ClusterRoleTemplateBinding().Create(crtbObj)
		require.NoError(s.T(), err, "Should create CRTB for SCIM user %s", userName)
		require.NoError(s.T(), rbacapi.WaitForCrtbStatus(s.client, crtb.Namespace, crtb.Name))

		_, err = rbacapi.VerifyClusterRoleTemplateBindingForUser(s.client, mgmtUser.ID, 1)
		require.NoError(s.T(), err, "Should find exactly 1 CRTB for SCIM user %s", userName)
	}
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMZZDisableAuthCleansUpGroupsAndToken() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Creating SCIM groups to verify they are deleted on auth provider disable")

	var groupIDs []string
	var groupNames []string
	for i := 0; i < 2; i++ {
		gName := namegen.AppendRandomString("scim-cleanup-group")
		gResp, err := s.scim.Groups().Create(scimactions.Group{
			Schemas:     []string{scimactions.SCIMSchemaGroup},
			DisplayName: gName,
		})
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusCreated, gResp.StatusCode)

		gID, err := gResp.IDFromBody()
		require.NoError(s.T(), err)
		groupIDs = append(groupIDs, gID)
		groupNames = append(groupNames, gName)
	}

	uResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: namegen.AppendRandomString("scim-cleanup-user"),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, uResp.StatusCode)

	userID, err := uResp.IDFromBody()
	require.NoError(s.T(), err)

	_, err = scimactions.FetchSCIMBearerToken(s.client, scimProvider)
	require.NoError(s.T(), err, "Token secret should exist before disabling auth provider")

	logrus.Info("Disabling OpenLDAP auth provider to trigger SCIM cleanup")
	err = s.client.Auth.OLDAP.Disable()
	require.NoError(s.T(), err, "Should be able to disable OpenLDAP")

	logrus.Info("Waiting for SCIM groups to be deleted by cleanup controller")
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.TwoMinuteTimeout, false,
		func(ctx context.Context) (bool, error) {
			for _, gID := range groupIDs {
				_, err := s.client.WranglerContext.Mgmt.Group().Get(gID, metav1.GetOptions{})
				if err == nil {
					return false, nil
				}
			}
			return true, nil
		},
	)
	require.NoError(s.T(), err, "All SCIM groups should be deleted after provider disable")

	logrus.Info("Verifying SCIM token secret is deleted")
	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false,
		func(ctx context.Context) (bool, error) {
			_, err := scimactions.FetchSCIMBearerToken(s.client, scimProvider)
			return err != nil, nil
		},
	)
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

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying DELETE /Groups/{id} for %s", groupName)

	createResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	groupID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)

	deleteResp, err := s.scim.Groups().Delete(groupID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNoContent, deleteResp.StatusCode, "DELETE /Groups/{id} should return 204, body: %s", string(deleteResp.Body))

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout, false, func(ctx context.Context) (bool, error) {
		getResp, pollErr := s.scim.Groups().ByID(groupID)
		if pollErr != nil {
			return false, pollErr
		}
		return getResp.StatusCode == http.StatusNotFound, nil
	})
	require.NoError(s.T(), err, "Group %s should return 404 after DELETE", groupID)
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMFilterGroupByDisplayName() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying GET /Groups?filter=displayName eq for %s", groupName)

	createResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	groupID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Groups().Delete(groupID) }()

	params := url.Values{}
	params.Set("filter", fmt.Sprintf("displayName eq %q", groupName))

	filterResp, err := s.scim.Groups().List(params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, filterResp.StatusCode, "Filtered GET /Groups should return 200, body: %s", string(filterResp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), filterResp.DecodeJSON(&body))

	resources, _ := body["Resources"].([]interface{})
	require.Len(s.T(), resources, 1, "Filter should return exactly 1 group")

	firstGroup, _ := resources[0].(map[string]interface{})
	require.Equal(s.T(), groupName, firstGroup["displayName"], "Returned group displayName should match filter")
	require.Equal(s.T(), groupID, firstGroup["id"])
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMGetGroupByIDExcludeMembersAttribute() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying GET /Groups/{id}?excludedAttributes=members for %s", groupName)

	createResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	groupID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Groups().Delete(groupID) }()

	params := url.Values{}
	params.Set("excludedAttributes", "members")

	resp, err := s.scim.Groups().ByIDWithQuery(groupID, params)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "GET /Groups/{id}?excludedAttributes=members should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), resp.DecodeJSON(&body))
	require.Equal(s.T(), groupID, body["id"])
	_, hasMembersField := body["members"]
	require.False(s.T(), hasMembersField, "members field should be absent when excludedAttributes=members")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchGroupReplaceExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying PATCH replace externalId for group %s", groupName)

	createResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	groupID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Groups().Delete(groupID) }()

	newExternalID := "ext-" + groupName

	patchResp, err := s.scim.Groups().Patch(groupID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "externalId", Value: newExternalID},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH replace externalId should return 200, body: %s", string(patchResp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), patchResp.DecodeJSON(&body))
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PATCH replace")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchUserReplaceExternalID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying PATCH replace externalId for user %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Users().Delete(userID) }()

	getResp, err := s.scim.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, getResp.StatusCode)

	newExternalID := "ext-" + userName

	patchResp, err := s.scim.Users().Patch(userID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "externalId", Value: newExternalID},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH replace externalId should return 200, body: %s", string(patchResp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), patchResp.DecodeJSON(&body))
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PATCH replace")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMPatchUserReplacePrimaryEmail() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying PATCH replace primary email for user %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Users().Delete(userID) }()

	getResp, err := s.scim.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, getResp.StatusCode)

	newEmail := userName + "@example.com"

	patchResp, err := s.scim.Users().Patch(userID, scimactions.PatchOp{
		Schemas: []string{scimactions.SCIMSchemaPatchOp},
		Operations: []scimactions.Operation{
			{Op: "replace", Path: "emails[primary eq true].value", Value: newEmail},
		},
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, patchResp.StatusCode, "PATCH replace primary email should return 200, body: %s", string(patchResp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), patchResp.DecodeJSON(&body))

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
		resp, err := s.scim.Discovery().ResourceTypeByID(resourceTypeID)
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusOK, resp.StatusCode, "GET /ResourceTypes/%s should return 200, body: %s", resourceTypeID, string(resp.Body))

		var body map[string]interface{}
		require.NoError(s.T(), resp.DecodeJSON(&body))
		require.Equal(s.T(), resourceTypeID, body["id"], "ResourceType id should match requested id")
		require.Equal(s.T(), resourceTypeID, body["name"])
	}

	notFoundResp, err := s.scim.Discovery().ResourceTypeByID("nonexistent")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNotFound, notFoundResp.StatusCode, "GET /ResourceTypes/nonexistent should return 404")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMSchemas() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Schemas returns User and Group schemas")

	resp, err := s.scim.Discovery().Schemas()
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "GET /Schemas should return 200, body: %s", string(resp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), resp.DecodeJSON(&body))
	require.Contains(s.T(), body, "totalResults")

	resources, _ := body["Resources"].([]interface{})
	require.GreaterOrEqual(s.T(), len(resources), 2, "Schemas should include at least User and Group")

	ids := map[string]bool{}
	for _, r := range resources {
		schema, _ := r.(map[string]interface{})
		id, _ := schema["id"].(string)
		ids[id] = true
	}
	require.True(s.T(), ids[scimactions.SCIMSchemaUser], "Schemas should include User schema")
	require.True(s.T(), ids[scimactions.SCIMSchemaGroup], "Schemas should include Group schema")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMSchemaByID() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying GET /Schemas/{id} for User and Group schemas")

	for _, schemaID := range []string{scimactions.SCIMSchemaUser, scimactions.SCIMSchemaGroup} {
		resp, err := s.scim.Discovery().SchemaByID(schemaID)
		require.NoError(s.T(), err)
		require.Equal(s.T(), http.StatusOK, resp.StatusCode, "GET /Schemas/%s should return 200, body: %s", schemaID, string(resp.Body))

		var body map[string]interface{}
		require.NoError(s.T(), resp.DecodeJSON(&body))
		require.Equal(s.T(), schemaID, body["id"], "Schema id should match requested id")
		require.Contains(s.T(), body, "attributes", "Schema should have attributes field")
	}

	notFoundResp, err := s.scim.Discovery().SchemaByID("urn:nonexistent")
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusNotFound, notFoundResp.StatusCode, "GET /Schemas/nonexistent should return 404")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUpdateGroup() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	groupName := namegen.AppendRandomString("scim-group")
	logrus.Infof("Verifying PUT /Groups/{id} for %s", groupName)

	createResp, err := s.scim.Groups().Create(scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		DisplayName: groupName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	groupID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Groups().Delete(groupID) }()

	newExternalID := "ext-" + groupName
	updateResp, err := s.scim.Groups().Update(groupID, scimactions.Group{
		Schemas:     []string{scimactions.SCIMSchemaGroup},
		ID:          groupID,
		DisplayName: groupName,
		ExternalID:  newExternalID,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, updateResp.StatusCode, "PUT /Groups/{id} should return 200, body: %s", string(updateResp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), updateResp.DecodeJSON(&body))
	require.Equal(s.T(), groupID, body["id"])
	require.Equal(s.T(), groupName, body["displayName"])
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PUT")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUpdateUser() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying PUT /Users/{id} for %s", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Users().Delete(userID) }()

	getResp, err := s.scim.Users().ByID(userID)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, getResp.StatusCode)

	newExternalID := "ext-" + userName
	updateResp, err := s.scim.Users().Update(userID, scimactions.User{
		Schemas:    []string{scimactions.SCIMSchemaUser},
		UserName:   userName,
		ExternalID: newExternalID,
		Active:     scimactions.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, updateResp.StatusCode, "PUT /Users/{id} should return 200, body: %s", string(updateResp.Body))

	var body map[string]interface{}
	require.NoError(s.T(), updateResp.DecodeJSON(&body))
	require.Equal(s.T(), userID, body["id"])
	require.Equal(s.T(), userName, body["userName"])
	require.Equal(s.T(), newExternalID, body["externalId"], "externalId should be updated after PUT")
}

func (s *SCIMOpenLDAPTestSuite) TestSCIMUserProjectRoleBinding() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	userName := namegen.AppendRandomString("scim-user")
	logrus.Infof("Verifying PRTB for SCIM user %s as project-owner", userName)

	createResp, err := s.scim.Users().Create(scimactions.User{
		Schemas:  []string{scimactions.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode)

	userID, err := createResp.IDFromBody()
	require.NoError(s.T(), err)
	defer func() { _, _ = s.scim.Users().Delete(userID) }()

	mgmtUser, err := s.client.Management.User.ByID(userID)
	require.NoError(s.T(), err, "Rancher user %s should exist after SCIM create", userID)

	project, err := s.client.WranglerContext.Mgmt.Project().Create(projectapi.NewProjectTemplate(s.cluster.ID))
	require.NoError(s.T(), err, "Should be able to create a project in cluster %s", s.cluster.ID)
	defer func() {
		_ = s.client.WranglerContext.Mgmt.Project().Delete(project.Namespace, project.Name, &metav1.DeleteOptions{})
	}()

	prtbNamespace := project.Name
	if project.Status.BackingNamespace != "" {
		prtbNamespace = fmt.Sprintf("%s-%s", project.Spec.ClusterName, project.Name)
	}

	projectName := fmt.Sprintf("%s:%s", project.Namespace, project.Name)

	prtbObj := &cattlev3.ProjectRoleTemplateBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      namegen.AppendRandomString("prtb-"),
			Namespace: prtbNamespace,
		},
		ProjectName:      projectName,
		UserName:         mgmtUser.ID,
		RoleTemplateName: "project-owner",
	}

	prtb, err := s.client.WranglerContext.Mgmt.ProjectRoleTemplateBinding().Create(prtbObj)
	require.NoError(s.T(), err, "Should be able to create PRTB for SCIM user %s", userName)
	require.NotEmpty(s.T(), prtb.Name)

	prtbs, err := rbacapi.VerifyProjectRoleTemplateBindingForUser(s.client, mgmtUser.ID, 1)
	require.NoError(s.T(), err, "Should find exactly 1 PRTB for SCIM user %s", userName)
	require.Equal(s.T(), projectName, prtbs[0].ProjectName, "PRTB should reference the correct project")
	require.Equal(s.T(), "project-owner", prtbs[0].RoleTemplateName, "PRTB role should be project-owner")
	require.Equal(s.T(), mgmtUser.ID, prtbs[0].UserName, "PRTB should be bound to the correct user")
}

func TestSCIMOpenLDAPSuite(t *testing.T) {
	suite.Run(t, new(SCIMOpenLDAPTestSuite))
}

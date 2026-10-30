//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"net/http"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsecretapi "github.com/rancher/shepherd/extensions/kubeapi/secrets"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	auditlogapi "github.com/rancher/tests/actions/kubeapi/auditlog"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	auditSidecarMissing    = "The Rancher server does not serve the audit log and this suite could not turn it on"
	scimOversizedBodyBytes = 1 << 21
)

type SCIMAuditLogTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	scimClient   *scimclient.Client
	auditEnabled bool
}

func (s *SCIMAuditLogTestSuite) SetupSuite() {
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

	logrus.Infof("Turning the Rancher audit log on at level %s", auditlogapi.RancherAuditLevel)
	restoreAudit, err := auditlogapi.EnsureRancherAuditLogging(s.client, auditlogapi.RancherAuditLevel)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on")
	s.session.RegisterCleanupFunc(restoreAudit)
	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the audit log change")

	sidecarPresent, err := auditlogapi.VerifyAuditLoggingEnabled(s.client)
	require.NoError(s.T(), err, "Failed to read the audit log configuration of the Rancher deployment")

	recordsBodies, err := auditlogapi.VerifyAuditLogRecordsBodies(s.client)
	require.NoError(s.T(), err, "Failed to read the audit level of the Rancher deployment")

	s.auditEnabled = sidecarPresent && recordsBodies
}

func (s *SCIMAuditLogTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditEntryNamesProviderAndToken() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a SCIM request is recorded with the provider and the token that authenticated it")

	_, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPost && entry.ResponseCode == http.StatusCreated && entry.User != nil
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM user creation")

	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The SCIM entry should name the provider as the user")
	require.Contains(s.T(), entry.User.Group, scimactions.SCIMAuditGroup, "The SCIM entry should carry the %s group", scimactions.SCIMAuditGroup)
	require.Equal(s.T(), []string{authactions.OpenLdap}, entry.User.Extra[scimactions.SCIMAuditProviderKey], "The SCIM entry should record the provider")

	tokenIDs := entry.User.Extra[scimactions.SCIMAuditTokenKey]
	require.Len(s.T(), tokenIDs, 1, "The SCIM entry should record exactly one token id")

	secretNames, err := scimactions.FetchSCIMTokenSecretNames(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Contains(s.T(), secretNames, tokenIDs[0], "The recorded token id should be the name of a SCIM token secret")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsRequestBody() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log keeps the body of a SCIM create request")

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPost && entry.RequestBody["userName"] == userName
	})
	require.NoError(s.T(), err, "The audit log should record the body of the SCIM create request for %s", userName)

	require.Equal(s.T(), userName, entry.RequestBody["userName"], "The recorded body should hold the created userName")
	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The entry holding the SCIM body should name the provider as the user")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRedactsSensitivePatchValues() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log redacts the value of a SCIM PATCH operation on a password attribute")

	password := namegen.AppendRandomString("scim-secret-value")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditPasswordPath, password)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	operations, ok := entry.RequestBody["Operations"].([]any)
	require.True(s.T(), ok, "The recorded PATCH body should hold an Operations array")
	require.Len(s.T(), operations, 1, "The recorded PATCH body should hold exactly one operation")

	operation, ok := operations[0].(map[string]any)
	require.True(s.T(), ok, "The recorded operation should be an object")
	require.Equal(s.T(), scimactions.SCIMAuditPasswordPath, operation["path"], "The recorded operation should keep its path")
	require.Equal(s.T(), scimactions.SCIMAuditRedacted, operation["value"], "The value of a password operation should be redacted")

	logs, err := auditlogapi.RancherAuditLogs(s.client)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), logs, password, "The audit log should never hold the value sent on a password attribute")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsEveryOperation() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log records every SCIM write method, not only create")

	userName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	putResp, err := s.scimClient.Users().Update(userID, scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: userName,
		Active:   scimclient.BoolPtr(true),
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(putResp, http.StatusOK, "PUT /Users/{id} should return 200"))

	patchResp, err := scimactions.PatchUser(s.scimClient, userID, "replace", "active", false)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(patchResp, http.StatusOK, "PATCH /Users/{id} should return 200"))

	deleteResp, err := s.scimClient.Users().Delete(userID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(deleteResp, http.StatusNoContent, "DELETE /Users/{id} should return 204"))

	userURI := scimactions.SCIMPathPrefix + authactions.OpenLdap + "/Users/" + userID
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		entry, err := auditlogapi.WaitForAuditLogEntry(s.client, userURI, func(entry auditlogapi.Entry) bool {
			return entry.Method == method
		})
		require.NoError(s.T(), err, "The audit log should record the SCIM %s of user %s", method, userID)
		require.NotNil(s.T(), entry.User, "The SCIM %s entry should name a user", method)
		require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The SCIM %s entry should name the provider as the user", method)
	}
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsRefusedRequests() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log records refused SCIM requests with their status")

	missingID := namegen.AppendRandomString("scim-absent")
	notFoundResp, err := s.scimClient.Users().ByID(missingID)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(notFoundResp, http.StatusNotFound, "GET /Users/{id} for an absent id should return 404"))

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	conflictResp, err := s.scimClient.Users().Create(scimclient.User{
		Schemas:  []string{scimclient.SCIMSchemaUser},
		UserName: userName,
	})
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(conflictResp, http.StatusConflict, "A duplicate POST /Users should return 409"))

	_, err = auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+missingID, func(entry auditlogapi.Entry) bool {
		return entry.ResponseCode == http.StatusNotFound
	})
	require.NoError(s.T(), err, "The audit log should record the refused GET with status 404")

	_, err = auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPost && entry.ResponseCode == http.StatusConflict
	})
	require.NoError(s.T(), err, "The audit log should record the refused create with status 409")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditNeverRecordsTokenValue() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log never holds the SCIM bearer token value")

	token, err := scimactions.FetchSCIMBearerToken(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.NotEmpty(s.T(), token)

	resp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusOK, "GET /Users should return 200"))

	_, err = auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodGet
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM list request")

	logs, err := auditlogapi.RancherAuditLogs(s.client)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), logs, token, "The audit log should identify the token by id, never by value")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditDoesNotAttributeUnauthenticatedRequests() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a rejected SCIM request is not recorded as the SCIM provider")

	badClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, "invalid-token")
	resp, err := badClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(resp, http.StatusUnauthorized, "Invalid token should return 401"))

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		return entry.ResponseCode == http.StatusUnauthorized
	})
	require.NoError(s.T(), err, "The audit log should record the rejected SCIM request")

	if entry.User == nil {
		return
	}

	require.NotEqual(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "A rejected request should not be attributed to the SCIM provider")
	require.NotContains(s.T(), entry.User.Group, scimactions.SCIMAuditGroup, "A rejected request should not carry the %s group", scimactions.SCIMAuditGroup)
	require.Empty(s.T(), entry.User.Extra[scimactions.SCIMAuditTokenKey], "A rejected request should not record a token id")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRedactsSensitivePatchValuesByURNPath() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log redacts a password value sent on the URN form of the attribute path")

	password := namegen.AppendRandomString("scim-secret-value")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditURNPasswordPath, password)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	operation, err := auditlogapi.FetchFirstPatchOperation(*entry)
	require.NoError(s.T(), err)
	require.Equal(s.T(), scimactions.SCIMAuditRedacted, operation["value"], "A password value sent on the URN path should be redacted")

	logs, err := auditlogapi.RancherAuditLogs(s.client)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), logs, password, "The audit log should never hold a password sent on the URN path")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRedactsEverySensitivePatchPath() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	for _, path := range []string{scimactions.SCIMAuditTokenPath, scimactions.SCIMAuditKubeconfigPath} {
		logrus.Infof("Verifying the audit log redacts the value of a SCIM PATCH operation on the %s attribute", path)

		secret := namegen.AppendRandomString("scim-secret-value")

		_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
		require.NoError(s.T(), err)

		_, err = scimactions.PatchUser(s.scimClient, userID, "replace", path, secret)
		require.NoError(s.T(), err)

		entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
			return entry.Method == http.MethodPatch
		})
		require.NoError(s.T(), err, "The audit log should record the SCIM PATCH on %s", path)

		operation, err := auditlogapi.FetchFirstPatchOperation(*entry)
		require.NoError(s.T(), err)
		require.Equal(s.T(), scimactions.SCIMAuditRedacted, operation["value"], "The value of a %s operation should be redacted", path)

		logs, err := auditlogapi.RancherAuditLogs(s.client)
		require.NoError(s.T(), err)
		require.NotContains(s.T(), logs, secret, "The audit log should never hold the value sent on %s", path)
	}
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditKeepsNonSensitivePatchValue() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log keeps the value of a SCIM PATCH operation on an attribute that holds no secret")

	displayName := namegen.AppendRandomString("scim-display")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, displayName)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	operation, err := auditlogapi.FetchFirstPatchOperation(*entry)
	require.NoError(s.T(), err)
	require.Equal(s.T(), displayName, operation["value"], "The value of a displayName operation should be recorded as it was sent")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRedactsAuthorizationHeader() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log records the Authorization header of a SCIM request as redacted")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, namegen.AppendRandomString("scim-display"))
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	authorization, ok := entry.RequestHeader[scimactions.SCIMAuthorizationHeader]
	require.True(s.T(), ok, "The recorded entry should hold an %s header", scimactions.SCIMAuthorizationHeader)
	require.Equal(s.T(), []string{scimactions.SCIMAuditRedacted}, authorization, "The %s header should be recorded as redacted", scimactions.SCIMAuthorizationHeader)
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditTokenIDNamesTheSecretThatMatched() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the audit log names the token secret a request authenticated with when the %s provider holds more than one", authactions.OpenLdap)

	secondName := namegen.AppendRandomString("scim-audit-token")

	tokenNamespace, err := scimactions.ResolveSCIMTokenNamespace(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Should be able to read which namespace the Rancher server takes SCIM tokens from")

	_, secondToken, err := scimactions.CreateSCIMTokenSecretInNamespace(s.client, authactions.OpenLdap, tokenNamespace, secondName)
	require.NoError(s.T(), err)
	subSession.RegisterCleanupFunc(func() error {
		return extsecretapi.DeleteSecret(s.client, extclusterapi.LocalCluster, tokenNamespace, secondName, false)
	})

	secondClient := scimactions.NewSCIMClientWithToken(s.client.RancherConfig.Host, authactions.OpenLdap, secondToken)
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(secondClient, http.StatusOK), "The second token should authenticate the SCIM endpoints")

	_, userID, err := scimactions.CreateSCIMUser(secondClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(secondClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, namegen.AppendRandomString("scim-display"))
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	require.Equal(s.T(), []string{secondName}, entry.User.Extra[scimactions.SCIMAuditTokenKey],
		"The entry should name the secret whose token the request carried, not another secret of the same provider")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditLevelResponseRecordsBothBodies() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the audit log records both SCIM bodies at level %s", auditlogapi.RancherAuditLevelResponse)

	restoreAudit, err := auditlogapi.EnsureRancherAuditLogging(s.client, auditlogapi.RancherAuditLevelResponse)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on at level %s", auditlogapi.RancherAuditLevelResponse)
	subSession.RegisterCleanupFunc(restoreAudit)

	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the audit level change")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	displayName := namegen.AppendRandomString("scim-display")
	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, displayName)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	require.NotEmpty(s.T(), entry.RequestBody, "Level %s should record the request body", auditlogapi.RancherAuditLevelResponse)
	require.NotEmpty(s.T(), entry.ResponseBody, "Level %s should record the response body", auditlogapi.RancherAuditLevelResponse)
	require.Equal(s.T(), userID, entry.ResponseBody["id"], "The recorded response body should describe the user the request patched")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditLevelRequestRecordsOnlyTheRequestBody() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the audit log records a SCIM request body but no response body at level %s", auditlogapi.RancherAuditLevel)

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	displayName := namegen.AppendRandomString("scim-display")
	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, displayName)
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	require.NotEmpty(s.T(), entry.RequestBody, "Level %s should record the request body", auditlogapi.RancherAuditLevel)
	require.Empty(s.T(), entry.ResponseBody, "Level %s should record no response body", auditlogapi.RancherAuditLevel)
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditLevelHeadersRecordsNoBodies() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the audit log records no SCIM bodies at level %s", auditlogapi.RancherAuditLevelHeaders)

	restoreAudit, err := auditlogapi.EnsureRancherAuditLogging(s.client, auditlogapi.RancherAuditLevelHeaders)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on at level %s", auditlogapi.RancherAuditLevelHeaders)
	subSession.RegisterCleanupFunc(restoreAudit)

	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the audit level change")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, namegen.AppendRandomString("scim-display"))
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should still record the SCIM PATCH of user %s", userID)

	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "Level %s should still name the provider", auditlogapi.RancherAuditLevelHeaders)
	require.Empty(s.T(), entry.RequestBody, "Level %s should record no request body", auditlogapi.RancherAuditLevelHeaders)
	require.Empty(s.T(), entry.ResponseBody, "Level %s should record no response body", auditlogapi.RancherAuditLevelHeaders)
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditOmitsGroupWhenGroupsAreExcluded() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a SCIM entry keeps its name and extra but carries no group when the Rancher server runs with %s", auditlogapi.RancherAuditExcludeGroupsEnv)

	restoreExcludeGroups, err := auditlogapi.EnsureRancherAuditExcludeGroups(s.client, "true")
	require.NoError(s.T(), err, "Failed to set %s on the Rancher deployment", auditlogapi.RancherAuditExcludeGroupsEnv)
	subSession.RegisterCleanupFunc(restoreExcludeGroups)

	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the audit configuration change")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, namegen.AppendRandomString("scim-display"))
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	require.Empty(s.T(), entry.User.Group, "The entry should carry no group when %s is set", auditlogapi.RancherAuditExcludeGroupsEnv)
	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The entry should still name the provider")
	require.Equal(s.T(), []string{authactions.OpenLdap}, entry.User.Extra[scimactions.SCIMAuditProviderKey], "The entry should still carry the provider extra")
	require.NotEmpty(s.T(), entry.User.Extra[scimactions.SCIMAuditTokenKey], "The entry should still carry the token id extra")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsBodySentAsPlainJSON() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the audit log records a SCIM PATCH body sent as %s rather than %s", scimactions.JSONContentType, scimactions.SCIMContentType)

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	displayName := namegen.AppendRandomString("scim-display")
	userPath := scimactions.SCIMPathPrefix + authactions.OpenLdap + "/Users/" + userID

	resp, err := scimactions.SendRawSCIMRequest(s.client, authactions.OpenLdap, http.MethodPatch, userPath, scimactions.JSONContentType,
		scimactions.NewSCIMPatchBody("replace", scimactions.SCIMAuditDisplayNamePath, displayName))
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "A SCIM PATCH sent as %s should still be served: %s", scimactions.JSONContentType, string(resp.Body))

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, userPath, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	operation, err := auditlogapi.FetchFirstPatchOperation(*entry)
	require.NoError(s.T(), err)
	require.Equal(s.T(), displayName, operation["value"], "The body of a request sent as %s should be recorded", scimactions.JSONContentType)
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsRequestThroughLocalClusterPath() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying a SCIM request through %s is recorded with the same identity and body", scimactions.SCIMLocalClusterPrefix)

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	displayName := namegen.AppendRandomString("scim-display")
	proxiedPath := scimactions.SCIMLocalClusterPrefix + scimactions.SCIMPathPrefix + authactions.OpenLdap + "/Users/" + userID

	resp, err := scimactions.SendRawSCIMRequest(s.client, authactions.OpenLdap, http.MethodPatch, proxiedPath, scimactions.SCIMContentType,
		scimactions.NewSCIMPatchBody("replace", scimactions.SCIMAuditDisplayNamePath, displayName))
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, resp.StatusCode, "A SCIM PATCH through %s should be served: %s", scimactions.SCIMLocalClusterPrefix, string(resp.Body))

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, proxiedPath, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH sent through %s", scimactions.SCIMLocalClusterPrefix)

	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "A proxied SCIM request should carry the same identity")
	require.Equal(s.T(), []string{authactions.OpenLdap}, entry.User.Extra[scimactions.SCIMAuditProviderKey], "A proxied SCIM request should carry the provider extra")

	operation, err := auditlogapi.FetchFirstPatchOperation(*entry)
	require.NoError(s.T(), err)
	require.Equal(s.T(), displayName, operation["value"], "A proxied SCIM request should have its body recorded")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditReportsAnOversizedBodyAsAnError() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log reports a body over the public API limit as an error and leaves Rancher serving SCIM")

	oversized := map[string]interface{}{
		"schemas":     []string{scimclient.SCIMSchemaUser},
		"userName":    namegen.AppendRandomString("scim-oversized"),
		"displayName": strings.Repeat("a", scimOversizedBodyBytes),
		"active":      true,
	}

	resp, err := scimactions.SendRawSCIMRequest(s.client, authactions.OpenLdap, http.MethodPost,
		scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", scimactions.SCIMContentType, oversized)
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusBadRequest, resp.StatusCode, "A body over the public API limit should be refused with 400")

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		_, reported := entry.RequestBody[auditlogapi.AuditLogErrorKey]
		return entry.Method == http.MethodPost && reported
	})
	require.NoError(s.T(), err, "The audit log should record the oversized SCIM create")

	require.Contains(s.T(), entry.RequestBody[auditlogapi.AuditLogErrorKey], "too large", "The entry should report the body as too large instead of holding it")

	_, _, err = scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err, "Rancher should keep serving SCIM after refusing an oversized body")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditLevelMetadataRecordsNoHeadersOrBodies() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Verifying the audit log records a SCIM request with metadata only at level %s", auditlogapi.RancherAuditLevelMetadata)

	restoreAudit, err := auditlogapi.EnsureRancherAuditLogging(s.client, auditlogapi.RancherAuditLevelMetadata)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on at level %s", auditlogapi.RancherAuditLevelMetadata)
	subSession.RegisterCleanupFunc(restoreAudit)

	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the audit level change")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimactions.SCIMAuditDisplayNamePath, namegen.AppendRandomString("scim-display"))
	require.NoError(s.T(), err)

	entry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should still record the SCIM PATCH of user %s", userID)

	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "Level %s should still name the provider", auditlogapi.RancherAuditLevelMetadata)
	require.Empty(s.T(), entry.RequestHeader, "Level %s should record no headers", auditlogapi.RancherAuditLevelMetadata)
	require.Empty(s.T(), entry.RequestBody, "Level %s should record no request body", auditlogapi.RancherAuditLevelMetadata)
	require.Empty(s.T(), entry.ResponseBody, "Level %s should record no response body", auditlogapi.RancherAuditLevelMetadata)
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRedactsPasswordSentOnCreateAndReplace() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log redacts a password sent in the body of a SCIM create and of a SCIM replace")

	createPassword := namegen.AppendRandomString("scim-create-secret")
	userName := namegen.AppendRandomString("scim-password-user")

	createResp, err := scimactions.SendRawSCIMRequest(s.client, authactions.OpenLdap, http.MethodPost,
		scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", scimactions.SCIMContentType, map[string]any{
			"schemas":                         []string{scimclient.SCIMSchemaUser},
			"userName":                        userName,
			scimactions.SCIMAuditPasswordPath: createPassword,
		})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusCreated, createResp.StatusCode, "A POST carrying a password should create the user: %s", string(createResp.Body))
	subSession.RegisterCleanupFunc(func() error {
		return scimactions.DeleteSCIMUserByUserName(s.scimClient, userName)
	})

	createEntry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPost && entry.RequestBody["userName"] == userName
	})
	require.NoError(s.T(), err, "The audit log should record the create of %s", userName)
	require.Equal(s.T(), scimactions.SCIMAuditRedacted, createEntry.RequestBody[scimactions.SCIMAuditPasswordPath],
		"The password sent on a create should be redacted")

	replacedName, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	replacePassword := namegen.AppendRandomString("scim-replace-secret")
	replaceResp, err := scimactions.SendRawSCIMRequest(s.client, authactions.OpenLdap, http.MethodPut,
		scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, scimactions.SCIMContentType, map[string]any{
			"schemas":                         []string{scimclient.SCIMSchemaUser},
			"userName":                        replacedName,
			scimactions.SCIMAuditPasswordPath: replacePassword,
		})
	require.NoError(s.T(), err)
	require.Equal(s.T(), http.StatusOK, replaceResp.StatusCode, "A PUT carrying a password should replace the user: %s", string(replaceResp.Body))

	replaceEntry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users/"+userID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPut
	})
	require.NoError(s.T(), err, "The audit log should record the replace of %s", userID)
	require.Equal(s.T(), scimactions.SCIMAuditRedacted, replaceEntry.RequestBody[scimactions.SCIMAuditPasswordPath],
		"The password sent on a replace should be redacted")

	logs, err := auditlogapi.RancherAuditLogs(s.client)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), logs, createPassword, "The audit log should never hold the password sent on a create")
	require.NotContains(s.T(), logs, replacePassword, "The audit log should never hold the password sent on a replace")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsGroupRequestBody() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log keeps the body of a SCIM group create and of a SCIM group patch")

	groupName, groupID, err := scimactions.CreateSCIMGroup(s.scimClient, subSession, "")
	require.NoError(s.T(), err)

	createEntry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Groups", func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPost && entry.RequestBody["displayName"] == groupName
	})
	require.NoError(s.T(), err, "The audit log should record the body of the SCIM group create for %s", groupName)
	require.Equal(s.T(), groupName, createEntry.RequestBody["displayName"], "The recorded body should hold the created displayName")
	require.Equal(s.T(), scimactions.SCIMAuditUserPrefix+authactions.OpenLdap, createEntry.User.Name, "The entry holding the SCIM group body should name the provider as the user")

	replacementName := namegen.AppendRandomString("scim-group-renamed")
	_, err = scimactions.PatchGroup(s.scimClient, groupID, "replace", "displayName", replacementName)
	require.NoError(s.T(), err)

	patchEntry, err := auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Groups/"+groupID, func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM group patch of %s", groupID)

	operation, err := auditlogapi.FetchFirstPatchOperation(*patchEntry)
	require.NoError(s.T(), err, "The recorded group patch body should hold an operation")
	require.Equal(s.T(), replacementName, operation["value"], "A group display name is not sensitive, so the recorded operation should keep its value")
}

func (s *SCIMAuditLogTestSuite) TestSCIMAuditRecordsRequestsRefusedByProviderState() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log records a SCIM request refused because the provider is disabled or paused")

	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap,
		map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigFalse}, http.StatusNotFound))
	subSession.RegisterCleanupFunc(func() error {
		return scimactions.RestoreSCIMBaseline(s.client, s.scimClient, authactions.OpenLdap)
	})

	disabledResp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(disabledResp, http.StatusNotFound, "A valid token should answer 404 while SCIM is disabled"))

	_, err = auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodGet && entry.ResponseCode == http.StatusNotFound
	})
	require.NoError(s.T(), err, "The audit log should record the request refused while SCIM is disabled with status 404")

	require.NoError(s.T(), scimactions.SetSCIMConfigMapAndWait(s.client, s.scimClient, authactions.OpenLdap,
		map[string]string{scimactions.SCIMConfigEnabledKey: scimactions.SCIMConfigTrue, scimactions.SCIMConfigPausedKey: scimactions.SCIMConfigTrue}, http.StatusServiceUnavailable))

	pausedResp, err := s.scimClient.Users().List(nil)
	require.NoError(s.T(), err)
	require.NoError(s.T(), scimactions.CheckStatus(pausedResp, http.StatusServiceUnavailable, "A valid token should answer 503 while SCIM is paused"))

	_, err = auditlogapi.WaitForAuditLogEntry(s.client, scimactions.SCIMPathPrefix+authactions.OpenLdap+"/Users", func(entry auditlogapi.Entry) bool {
		return entry.Method == http.MethodGet && entry.ResponseCode == http.StatusServiceUnavailable
	})
	require.NoError(s.T(), err, "The audit log should record the request refused while SCIM is paused with status 503")
}

func TestSCIMAuditLogSuite(t *testing.T) {
	suite.Run(t, new(SCIMAuditLogTestSuite))
}

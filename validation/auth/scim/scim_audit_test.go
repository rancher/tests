//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package scim

import (
	"net/http"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	scimclient "github.com/rancher/shepherd/clients/rancher/auth/scim"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	scimactions "github.com/rancher/tests/actions/kubeapi/scim"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	scimAuditUserPrefix   = "system:scim:"
	scimAuditGroup        = "system:scim"
	scimAuditProviderKey  = "scim.cattle.io/provider"
	scimAuditTokenKey     = "scim.cattle.io/token-id"
	scimAuditRedacted     = "[redacted]"
	scimAuditPasswordPath = "password"

	auditSidecarMissing = "The Rancher server does not serve the audit log and this suite could not turn it on"
)

type SCIMAuditTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	scimClient   *scimclient.Client
	scimURI      string
	auditEnabled bool
	restoreAudit func() error
}

func (s *SCIMAuditTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")
	s.client = client

	logrus.Info("Setting up SCIM client for OpenLDAP provider")
	scimClient, err := scimactions.SetupSCIMClient(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err, "Failed to setup SCIM client")
	s.scimClient = scimClient
	s.scimURI = "/v1-scim/" + authactions.OpenLdap

	require.NoError(s.T(), scimactions.VerifyExpectedBuildType(s.client), "The Rancher build type should match the one the config expects")

	servable, reason, err := scimactions.VerifySCIMServable(s.client)
	require.NoError(s.T(), err, "Failed to read whether this Rancher server serves SCIM")
	if !servable {
		s.T().Skip(reason)
	}

	logrus.Info("Ensuring baseline SCIM ConfigMap (enabled=true) for OpenLDAP provider")
	require.NoError(s.T(), scimactions.SetSCIMConfigMap(s.client, authactions.OpenLdap, scimactions.BaselineSCIMConfigMap()))
	require.NoError(s.T(), scimactions.WaitForSCIMEndpointStatus(s.scimClient, http.StatusOK))

	logrus.Infof("Turning the Rancher audit log on at level %s", authactions.RancherAuditLevel)
	s.restoreAudit, err = authactions.EnsureRancherAuditLogging(s.client, authactions.RancherAuditLevel)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on")
	require.NoError(s.T(), scimactions.WaitForRancherRestart(s.client, s.scimClient), "Rancher should be fully operational after the audit log change")

	sidecarPresent, err := authactions.VerifyAuditLoggingEnabled(s.client)
	require.NoError(s.T(), err, "Failed to read the audit log configuration of the Rancher deployment")

	recordsBodies, err := authactions.VerifyAuditLogRecordsBodies(s.client)
	require.NoError(s.T(), err, "Failed to read the audit level of the Rancher deployment")

	s.auditEnabled = sidecarPresent && recordsBodies
}

func (s *SCIMAuditTestSuite) TearDownSuite() {
	if s.restoreAudit != nil {
		if err := s.restoreAudit(); err != nil {
			logrus.WithError(err).Warn("Failed to restore the audit log configuration of the Rancher deployment")
		}
	}
	s.session.Cleanup()
}

func (s *SCIMAuditTestSuite) TestSCIMAuditEntryNamesProviderAndToken() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying a SCIM request is recorded with the provider and the token that authenticated it")

	_, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	entry, err := authactions.WaitForAuditLogEntry(s.client, s.scimURI, func(entry authactions.AuditLogEntry) bool {
		return entry.Method == http.MethodPost && entry.ResponseCode == http.StatusCreated && entry.User != nil
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM user creation")

	require.Equal(s.T(), scimAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The SCIM entry should name the provider as the user")
	require.Contains(s.T(), entry.User.Group, scimAuditGroup, "The SCIM entry should carry the %s group", scimAuditGroup)
	require.Equal(s.T(), []string{authactions.OpenLdap}, entry.User.Extra[scimAuditProviderKey], "The SCIM entry should record the provider")

	tokenIDs := entry.User.Extra[scimAuditTokenKey]
	require.Len(s.T(), tokenIDs, 1, "The SCIM entry should record exactly one token id")

	secretNames, err := scimactions.FetchSCIMTokenSecretNames(s.client, authactions.OpenLdap)
	require.NoError(s.T(), err)
	require.Contains(s.T(), secretNames, tokenIDs[0], "The recorded token id should be the name of a SCIM token secret")
}

func (s *SCIMAuditTestSuite) TestSCIMAuditRecordsRequestBody() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log keeps the body of a SCIM create request")

	userName, _, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	entry, err := authactions.WaitForAuditLogEntry(s.client, s.scimURI, func(entry authactions.AuditLogEntry) bool {
		return entry.Method == http.MethodPost && entry.RequestBody["userName"] == userName
	})
	require.NoError(s.T(), err, "The audit log should record the body of the SCIM create request for %s", userName)

	require.Equal(s.T(), userName, entry.RequestBody["userName"], "The recorded body should hold the created userName")
	require.Equal(s.T(), scimAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The entry holding the SCIM body should name the provider as the user")
}

func (s *SCIMAuditTestSuite) TestSCIMAuditRedactsSensitivePatchValues() {
	if !s.auditEnabled {
		s.T().Skip(auditSidecarMissing)
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Verifying the audit log redacts the value of a SCIM PATCH operation on a password attribute")

	password := namegen.AppendRandomString("scim-secret-value")

	_, userID, err := scimactions.CreateSCIMUser(s.scimClient, subSession, "", true)
	require.NoError(s.T(), err)

	_, err = scimactions.PatchUser(s.scimClient, userID, "replace", scimAuditPasswordPath, password)
	require.NoError(s.T(), err)

	entry, err := authactions.WaitForAuditLogEntry(s.client, s.scimURI+"/Users/"+userID, func(entry authactions.AuditLogEntry) bool {
		return entry.Method == http.MethodPatch
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM PATCH of user %s", userID)

	operations, ok := entry.RequestBody["Operations"].([]any)
	require.True(s.T(), ok, "The recorded PATCH body should hold an Operations array")
	require.Len(s.T(), operations, 1, "The recorded PATCH body should hold exactly one operation")

	operation, ok := operations[0].(map[string]any)
	require.True(s.T(), ok, "The recorded operation should be an object")
	require.Equal(s.T(), scimAuditPasswordPath, operation["path"], "The recorded operation should keep its path")
	require.Equal(s.T(), scimAuditRedacted, operation["value"], "The value of a password operation should be redacted")

	logs, err := authactions.RancherAuditLogs(s.client)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), logs, password, "The audit log should never hold the value sent on a password attribute")
}

func (s *SCIMAuditTestSuite) TestSCIMAuditRecordsEveryOperation() {
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

	userURI := s.scimURI + "/Users/" + userID
	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		entry, err := authactions.WaitForAuditLogEntry(s.client, userURI, func(entry authactions.AuditLogEntry) bool {
			return entry.Method == method
		})
		require.NoError(s.T(), err, "The audit log should record the SCIM %s of user %s", method, userID)
		require.NotNil(s.T(), entry.User, "The SCIM %s entry should name a user", method)
		require.Equal(s.T(), scimAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "The SCIM %s entry should name the provider as the user", method)
	}
}

func (s *SCIMAuditTestSuite) TestSCIMAuditRecordsRefusedRequests() {
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

	_, err = authactions.WaitForAuditLogEntry(s.client, s.scimURI+"/Users/"+missingID, func(entry authactions.AuditLogEntry) bool {
		return entry.ResponseCode == http.StatusNotFound
	})
	require.NoError(s.T(), err, "The audit log should record the refused GET with status 404")

	_, err = authactions.WaitForAuditLogEntry(s.client, s.scimURI+"/Users", func(entry authactions.AuditLogEntry) bool {
		return entry.Method == http.MethodPost && entry.ResponseCode == http.StatusConflict
	})
	require.NoError(s.T(), err, "The audit log should record the refused create with status 409")
}

func (s *SCIMAuditTestSuite) TestSCIMAuditNeverRecordsTokenValue() {
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

	_, err = authactions.WaitForAuditLogEntry(s.client, s.scimURI+"/Users", func(entry authactions.AuditLogEntry) bool {
		return entry.Method == http.MethodGet
	})
	require.NoError(s.T(), err, "The audit log should record the SCIM list request")

	logs, err := authactions.RancherAuditLogs(s.client)
	require.NoError(s.T(), err)
	require.NotContains(s.T(), logs, token, "The audit log should identify the token by id, never by value")
}

func (s *SCIMAuditTestSuite) TestSCIMAuditDoesNotAttributeUnauthenticatedRequests() {
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

	entry, err := authactions.WaitForAuditLogEntry(s.client, s.scimURI+"/Users", func(entry authactions.AuditLogEntry) bool {
		return entry.ResponseCode == http.StatusUnauthorized
	})
	require.NoError(s.T(), err, "The audit log should record the rejected SCIM request")

	if entry.User == nil {
		return
	}

	require.NotEqual(s.T(), scimAuditUserPrefix+authactions.OpenLdap, entry.User.Name, "A rejected request should not be attributed to the SCIM provider")
	require.NotContains(s.T(), entry.User.Group, scimAuditGroup, "A rejected request should not carry the %s group", scimAuditGroup)
	require.Empty(s.T(), entry.User.Extra[scimAuditTokenKey], "A rejected request should not record a token id")
}

func TestSCIMAuditSuite(t *testing.T) {
	suite.Run(t, new(SCIMAuditTestSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11  && !2.12  && !2.13  && !2.14

package tokens

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/settings"
	exttokenapi "github.com/rancher/tests/actions/tokens/exttokens"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LegacyTokenManagerExtTokenCompatibilityTest struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	defaultExtTokenTTL int64
}

func (t *LegacyTokenManagerExtTokenCompatibilityTest) TearDownSuite() {
	t.session.Cleanup()
}

func (t *LegacyTokenManagerExtTokenCompatibilityTest) SetupSuite() {
	t.session = session.NewSession()

	client, err := rancher.NewClient("", t.session)
	require.NoError(t.T(), err)
	t.client = client

	log.Info("Getting default TTL value to be used in tests")
	defaultTTLString, err := settings.GetGlobalSettingDefaultValue(t.client, settings.AuthTokenMaxTTLMinutes)
	require.NoError(t.T(), err)
	defaultTTLInt, err := strconv.Atoi(defaultTTLString)
	require.NoError(t.T(), err)
	defaultTTL := int64(defaultTTLInt * 60000)
	t.defaultExtTokenTTL = defaultTTL
}

func (t *LegacyTokenManagerExtTokenCompatibilityTest) TestAuthenticateWithLegacyTokenManagerUsingExtToken() {
	subSession := t.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext token as standard user")
	_, standardUserClient, err := rbac.SetupUser(t.client, rbac.StandardUser.String())
	require.NoError(t.T(), err)
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, t.defaultExtTokenTTL)
	require.NoError(t.T(), err)

	log.Info("Create an R_SESS cookie containing the ext token value and use it to authenticate a request against the legacy token manager endpoint")
	standardUserTokenValue := standardUserExtToken.Status.Value
	baseURL := fmt.Sprintf("https://%s", standardUserClient.RancherConfig.Host)
	legacyTokenAPIPath := "/v3/tokens/"
	err = exttokenapi.AuthenticateWithExtToken(baseURL, standardUserExtToken.Name, standardUserTokenValue, legacyTokenAPIPath)
	require.NoError(t.T(), err)
}

func (t *LegacyTokenManagerExtTokenCompatibilityTest) TestDeleteLegacyTokenUsingExtToken() {
	subSession := t.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create legacy token for the standard user")
	_, standardUserClient, err := rbac.SetupUser(t.client, rbac.StandardUser.String())
	require.NoError(t.T(), err)
	standardUserLegacyToken, err := standardUserClient.Management.Token.Create(&management.Token{
		Description: "legacy token for standard user",
		UserID:      standardUserClient.UserID,
	})
	require.NoError(t.T(), err)
	log.Infof("Legacy token %s created for standard user %s", standardUserLegacyToken.ID, standardUserClient.UserID)

	log.Info("Create ext token for the standard user")
	standardUserExtToken, err := exttokenapi.CreateExtToken(standardUserClient, t.defaultExtTokenTTL)
	require.NoError(t.T(), err)

	log.Info("Using the standard users ext token send a request to delete the standard users legacy token")
	err = exttokenapi.DeleteLegacyTokenWithExtToken(standardUserClient, standardUserLegacyToken.ID, standardUserExtToken.Status.BearerToken)
	require.NoError(t.T(), err)
}

func TestLegacyTokenManagerExtTokenCompatibilityTest(t *testing.T) {
	suite.Run(t, new(LegacyTokenManagerExtTokenCompatibilityTest))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package ttl

import (
	"context"
	"strconv"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigapi "github.com/rancher/tests/actions/kubeapi/kubeconfigs"
	rancherserverapi "github.com/rancher/tests/actions/kubeapi/rancherserver"
	settingsapi "github.com/rancher/tests/actions/kubeapi/settings"
	tokenapi "github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	deploymentsapi "github.com/rancher/tests/actions/kubeapi/workloads/deployments"
	"github.com/rancher/tests/actions/rbac"
	v3tokenapi "github.com/rancher/tests/actions/tokens"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

type TokenTTLAdditionalChecksTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (a *TokenTTLAdditionalChecksTestSuite) TearDownSuite() {
	a.session.Cleanup()
}

func (a *TokenTTLAdditionalChecksTestSuite) SetupSuite() {
	a.session = session.NewSession()

	client, err := rancher.NewClient("", a.session)
	require.NoError(a.T(), err)
	a.client = client
}

func (a *TokenTTLAdditionalChecksTestSuite) TestNegativeTTLOnGeneralTokens() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying how a negative TTL behaves for general ext and v3 tokens, with and without a max TTL configured")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	extToken, err := tokenapi.CreateExtToken(a.client, -1)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(604800000), extToken.Spec.TTL)

	v3Token, err := v3tokenapi.CreateV3Token(a.client, -1)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(604800000), v3Token.TTLMillis)

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))

	unlimitedExtToken, err := tokenapi.CreateExtToken(a.client, -1)
	require.NoError(a.T(), err)
	require.NoError(a.T(), tokenapi.VerifyNoExpiry(unlimitedExtToken.Status.ExpiresAt))

	unlimitedV3Token, err := v3tokenapi.CreateV3Token(a.client, -1)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(0), unlimitedV3Token.TTLMillis)
	require.False(a.T(), unlimitedV3Token.Expired)
	require.NoError(a.T(), tokenapi.VerifyTokenAuthenticates(unlimitedV3Token.Token))
}

func (a *TokenTTLAdditionalChecksTestSuite) TestTokenStopsWorkingAfterExpiry() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that short-lived tokens stop authenticating after they expire while a longer-lived token remains valid")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	tokenA, err := tokenapi.CreateExtToken(a.client, 120000)
	require.NoError(a.T(), err)
	tokenB, err := v3tokenapi.CreateV3Token(a.client, 120000)
	require.NoError(a.T(), err)
	tokenC, err := tokenapi.CreateExtToken(a.client, 3600000)
	require.NoError(a.T(), err)
	credentialA, credentialB, credentialC := tokenA.Status.BearerToken, tokenB.Token, tokenC.Status.BearerToken

	for name, credential := range map[string]string{"A": credentialA, "B": credentialB, "C": credentialC} {
		require.NoErrorf(a.T(), tokenapi.VerifyTokenAuthenticates(credential), "token %s should authenticate before expiry", name)
	}

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		if tokenapi.VerifyTokenAuthenticates(credentialA) == nil {
			return false, nil
		}
		if tokenapi.VerifyTokenAuthenticates(credentialB) == nil {
			return false, nil
		}
		return true, nil
	})
	require.NoError(a.T(), err, "expected tokens A and B to stop authenticating after expiry")

	require.NoError(a.T(), tokenapi.VerifyTokenAuthenticates(credentialC))
}

func (a *TokenTTLAdditionalChecksTestSuite) TestSessionTokensAreNotAffected() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the general and kubeconfig max TTL settings do not shorten a login (session) token's TTL")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(60, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(60, 10)))

	user, userClient, err := rbac.SetupUser(a.client, rbac.StandardUser.String())
	require.NoError(a.T(), err)

	loginToken, err := v3tokenapi.GetLoginV3Token(userClient, user.ID)
	require.NoError(a.T(), err)
	require.False(a.T(), loginToken.IsDerived, "expected the login token, not a user-created derived token")
	require.Equal(a.T(), int64(57600000), loginToken.TTLMillis)
	require.NotEmpty(a.T(), loginToken.ExpiresAt)
}

func (a *TokenTTLAdditionalChecksTestSuite) TestAccessControl() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a standard user cannot change the TTL settings and still has token TTLs capped by the admin-configured max")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	_, standardUserClient, err := rbac.SetupUser(a.client, rbac.StandardUser.String())
	require.NoError(a.T(), err)

	originalDefault, err := extsettingsapi.GetGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes)
	require.NoError(a.T(), err)
	originalKubeconfigMax, err := extsettingsapi.GetGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes)
	require.NoError(a.T(), err)

	err = extsettingsapi.UpdateGlobalSetting(standardUserClient, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(60, 10))
	require.Error(a.T(), err)
	require.True(a.T(), k8serrors.IsForbidden(err), "expected HTTP 403, got: %s", err)

	err = extsettingsapi.UpdateGlobalSetting(standardUserClient, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(60, 10))
	require.Error(a.T(), err)
	require.True(a.T(), k8serrors.IsForbidden(err), "expected HTTP 403, got: %s", err)

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, originalDefault))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, originalKubeconfigMax))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	v3Token, err := v3tokenapi.CreateV3Token(standardUserClient, 1209600000)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(604800000), v3Token.TTLMillis)
	require.NotEqual(a.T(), int64(1209600000), v3Token.TTLMillis)

	extToken, err := tokenapi.CreateExtToken(standardUserClient, 1209600000)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(604800000), extToken.Spec.TTL)
}

func (a *TokenTTLAdditionalChecksTestSuite) TestNewSettingsHaveTheExpectedInstallationDefaults() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the four token TTL settings exist with their expected installation default values and empty values")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	authDefaultValue, err := extsettingsapi.GetGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes)
	require.NoError(a.T(), err)
	require.Empty(a.T(), authDefaultValue)
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingDefaultValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "129600"))

	authMaxValue, err := extsettingsapi.GetGlobalSettingValue(a.client, settingsapi.AuthTokenMaxTTLMinutes)
	require.NoError(a.T(), err)
	require.Empty(a.T(), authMaxValue)
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingDefaultValue(a.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))

	kubeconfigDefaultValue, err := extsettingsapi.GetGlobalSettingValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes)
	require.NoError(a.T(), err)
	require.Empty(a.T(), kubeconfigDefaultValue)
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingDefaultValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "43200"))

	kubeconfigMaxValue, err := extsettingsapi.GetGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes)
	require.NoError(a.T(), err)
	require.Empty(a.T(), kubeconfigMaxValue)
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingDefaultValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, "129600"))
}

func (a *TokenTTLAdditionalChecksTestSuite) TestChangedSettingsPersistAfterRancherRestarts() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that custom TTL setting values persist across a Rancher server restart")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(4320, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	previousRestartCounts, err := rancherserverapi.FetchRancherServerRestartCounts(a.client)
	require.NoError(a.T(), err)
	require.NoError(a.T(), deploymentsapi.RestartDeployment(a.client, extclusterapi.LocalCluster, deploymentsapi.RancherDeploymentNamespace, deploymentsapi.RancherDeploymentName))

	err = kwait.PollUntilContextTimeout(context.Background(), defaults.TenSecondTimeout, defaults.FifteenMinuteTimeout, true, func(ctx context.Context) (bool, error) {
		return rancherserverapi.VerifyRancherServerRestarted(a.client, previousRestartCounts)
	})
	require.NoError(a.T(), err, "expected the Rancher server to restart")

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "4320"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(a.client, 0)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(86400000), token.Spec.TTL)
}

func (a *TokenTTLAdditionalChecksTestSuite) TestSessionKindExtTokenFollowsTheSessionSetting() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that session-kind tokens follow the session TTL setting rather than the general default/max")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	require.NoError(a.T(), tokenapi.VerifySessionTokenCreateRejected(a.client))

	user, userClient, err := rbac.SetupUser(a.client, rbac.StandardUser.String())
	require.NoError(a.T(), err)
	loginToken, err := v3tokenapi.GetLoginV3Token(userClient, user.ID)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(57600000), loginToken.TTLMillis)
	require.NotEqual(a.T(), int64(86400000), loginToken.TTLMillis)
}

func (a *TokenTTLAdditionalChecksTestSuite) TestKubectlExplainDocumentsTheNewTokenTTLSettings() {
	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that kubectl explain documents the new token TTL settings and how they interact")

	explainCmd := []string{"kubectl", "explain", "tokens.ext.cattle.io.spec.ttl"}
	description, err := kubectl.Command(a.client, nil, extclusterapi.LocalCluster, explainCmd, "")
	require.NoError(a.T(), err, "Error executing 'kubectl explain tokens.ext.cattle.io.spec.ttl' command")
	log.Info("Verify the new settings kubeconfig-max-token-ttl-minutes and auth-token-default-ttl-minutes are documented")
	require.Contains(a.T(), description, settingsapi.KubeconfigMaxTTLMinutes)
	require.Contains(a.T(), description, settingsapi.AuthTokenDefaultTTLMinutes)
	require.Contains(a.T(), description, settingsapi.AuthTokenMaxTTLMinutes)
	require.Contains(a.T(), description, settingsapi.KubeconfigDefaultTTLMinutes)
	require.Contains(a.T(), description, settingsapi.AuthUserSessionTTLMinutes)
}

// Skipped Test - GH#58662
func (a *TokenTTLAdditionalChecksTestSuite) TestVeryLargeFiniteTTLsAcceptedWhenMaxIsUnlimited() {
	a.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58662")

	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that very large, but finite, TTLs are honored for ext, v3 and kubeconfig tokens when the max TTL settings are unlimited")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))

	hugeTTLMillis := int64(10000000000000)
	extToken, err := tokenapi.CreateExtToken(a.client, hugeTTLMillis)
	require.NoError(a.T(), err)
	require.Equal(a.T(), hugeTTLMillis, extToken.Spec.TTL)
	require.NoError(a.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(extToken.Status.ExpiresAt, hugeTTLMillis))
	require.False(a.T(), extToken.Status.Expired)
	require.NoError(a.T(), tokenapi.VerifyTokenAuthenticates(extToken.Status.BearerToken))

	v3Token, err := v3tokenapi.CreateV3Token(a.client, hugeTTLMillis)
	require.NoError(a.T(), err)
	require.Equal(a.T(), hugeTTLMillis, v3Token.TTLMillis)
	require.NoError(a.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(v3Token.ExpiresAt, hugeTTLMillis))
	require.False(a.T(), v3Token.Expired)
	require.NoError(a.T(), tokenapi.VerifyTokenAuthenticates(v3Token.Token))

	hugeTTLSeconds := int64(10000000000)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(a.client, []string{extclusterapi.LocalCluster}, "", &hugeTTLSeconds)
	require.NoError(a.T(), err)
	require.Equal(a.T(), hugeTTLSeconds, kubeconfig.Spec.TTL)
	require.NoError(a.T(), kubeconfigapi.WaitForBackingExtTokenCreation(a.client, kubeconfig.Name))
	backingTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(a.client, kubeconfig.Name)
	require.NoError(a.T(), err)
	require.NotEmpty(a.T(), backingTokens.Items, "expected at least one backing token for kubeconfig %s", kubeconfig.Name)
	backingToken := backingTokens.Items[0]
	require.Equal(a.T(), hugeTTLMillis, backingToken.Spec.TTL)
	require.NoError(a.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(backingToken.Status.ExpiresAt, hugeTTLMillis))
	require.False(a.T(), backingToken.Status.Expired)
	require.NoError(a.T(), tokenapi.VerifyTokenAuthenticates(backingToken.Status.BearerToken))

	moderatelyLargeTTLMillis := int64(300000000000)
	secondExtToken, err := tokenapi.CreateExtToken(a.client, moderatelyLargeTTLMillis)
	require.NoError(a.T(), err)
	require.Equal(a.T(), moderatelyLargeTTLMillis, secondExtToken.Spec.TTL)
	require.NoError(a.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(secondExtToken.Status.ExpiresAt, moderatelyLargeTTLMillis))
	require.False(a.T(), secondExtToken.Status.Expired)
	require.NoError(a.T(), tokenapi.VerifyTokenAuthenticates(secondExtToken.Status.BearerToken))
}

// Skipped Test - GH#58668
func (a *TokenTTLAdditionalChecksTestSuite) TestInvalidAuthTokenDefaultTTLMinutesRejected() {
	a.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58668")

	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an invalid auth-token-default-ttl-minutes value is rejected and the last valid value keeps applying")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))

	err := extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "abc")
	require.Error(a.T(), err)
	require.True(a.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(a.T(), err.Error(), "abc")

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))

	token, err := tokenapi.CreateExtToken(a.client, 0)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(86400000), token.Spec.TTL)
}

// Skipped Test - GH#58668
func (a *TokenTTLAdditionalChecksTestSuite) TestInvalidAuthTokenMaxTTLMinutesRejected() {
	a.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58668")

	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an invalid auth-token-max-ttl-minutes value is rejected and the last valid value keeps applying")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	err := extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.AuthTokenMaxTTLMinutes, "abc")
	require.Error(a.T(), err)
	require.True(a.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(a.T(), err.Error(), "abc")

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	threeDaysMillis := int64(259200000)
	token, err := tokenapi.CreateExtToken(a.client, threeDaysMillis)
	require.NoError(a.T(), err)
	require.Equal(a.T(), threeDaysMillis, token.Spec.TTL)
}

// Skipped Test - GH#58668
func (a *TokenTTLAdditionalChecksTestSuite) TestInvalidKubeconfigDefaultTTLMinutesRejected() {
	a.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58668")

	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an invalid kubeconfig-default-token-ttl-minutes value is rejected and the last valid value keeps applying")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	err := extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "abc")
	require.Error(a.T(), err)
	require.True(a.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(a.T(), err.Error(), "abc")

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(a.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(a.T(), err)
	require.Equal(a.T(), int64(86400), kubeconfig.Spec.TTL)

	require.NoError(a.T(), kubeconfigapi.WaitForBackingExtTokenCreation(a.client, kubeconfig.Name))
	backingTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(a.client, kubeconfig.Name)
	require.NoError(a.T(), err)
	require.NotEmpty(a.T(), backingTokens.Items, "expected at least one backing token for kubeconfig %s", kubeconfig.Name)
	require.Equal(a.T(), int64(86400000), backingTokens.Items[0].Spec.TTL)
}

// Skipped Test - GH#58668
func (a *TokenTTLAdditionalChecksTestSuite) TestInvalidKubeconfigMaxTTLMinutesRejected() {
	a.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58668")

	subSession := a.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an invalid kubeconfig-max-token-ttl-minutes value is rejected and the last valid value keeps applying")

	require.NoError(a.T(), settingsapi.ResetTokenTTLSettingsToDefault(a.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	err := extsettingsapi.UpdateGlobalSetting(a.client, settingsapi.KubeconfigMaxTTLMinutes, "abc")
	require.Error(a.T(), err)
	require.True(a.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(a.T(), err.Error(), "abc")

	require.NoError(a.T(), settingsapi.VerifyGlobalSettingValue(a.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	threeDaysSeconds := int64(259200)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(a.client, []string{extclusterapi.LocalCluster}, "", &threeDaysSeconds)
	require.NoError(a.T(), err)

	require.NoError(a.T(), kubeconfigapi.WaitForBackingExtTokenCreation(a.client, kubeconfig.Name))
	backingTokens, err := kubeconfigapi.GetBackingExtTokensForKubeconfig(a.client, kubeconfig.Name)
	require.NoError(a.T(), err)
	require.NotEmpty(a.T(), backingTokens.Items, "expected at least one backing token for kubeconfig %s", kubeconfig.Name)
	require.Equal(a.T(), int64(259200000), backingTokens.Items[0].Spec.TTL)
}

func TestTokenTTLAdditionalChecksTestSuite(t *testing.T) {
	suite.Run(t, new(TokenTTLAdditionalChecksTestSuite))
}

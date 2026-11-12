//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package ttl

import (
	"strconv"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/pkg/session"
	settingsapi "github.com/rancher/tests/actions/kubeapi/settings"
	tokenapi "github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ExtTokenTTLTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (e *ExtTokenTTLTestSuite) TearDownSuite() {
	e.session.Cleanup()
}

func (e *ExtTokenTTLTestSuite) SetupSuite() {
	e.session = session.NewSession()

	client, err := rancher.NewClient("", e.session)
	require.NoError(e.T(), err)
	e.client = client
}

func (e *ExtTokenTTLTestSuite) TestDefaultTTLAppliesToNewToken() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an ext token created without spec.ttl receives the configured default TTL")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), tokenapi.MinutesToMilliseconds(1440), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestExplicitTTLBelowMaxRespected() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl below the max is respected rather than replaced by the default")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 3600000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(3600000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestExplicitTTLExceedingMaxCapped() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl above the max is capped at the max")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 1209600000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 7*24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestDefaultTTLGreaterThanMaxReducedToMax() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default TTL greater than the max is reduced to the max")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(20160, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "20160"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 7*24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestDefaultTTLOfZeroUsesMax() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default TTL of 0 resolves to the configured max")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "0"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 7*24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestMaxOfZeroAllowsTokenLongerThanDefault() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a max of 0 allows an explicit TTL longer than the default")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	token, err := tokenapi.CreateExtToken(e.client, 2592000000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(2592000000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 30*24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestDefaultAndMaxBothZeroAllowTokenWithoutExpiration() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default and max of 0 together produce a non-expiring ext token that still authenticates")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "0"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.NoError(e.T(), tokenapi.VerifyNoExpiry(token.Status.ExpiresAt))
	require.NoError(e.T(), tokenapi.VerifyTokenAuthenticates(token.Status.BearerToken))
}

func (e *ExtTokenTTLTestSuite) TestExplicitTTLEqualToMaxAccepted() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl equal to the max is accepted unchanged")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 604800000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 7*24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestInstallationDefaultTTLForGeneralToken() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an ext token created at the installation default TTL settings receives the expected default TTL")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	defaultTTLValue, err := extsettingsapi.GetGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes)
	require.NoError(e.T(), err)
	require.Empty(e.T(), defaultTTLValue)
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingDefaultValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "129600"))

	maxTTLValue, err := extsettingsapi.GetGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes)
	require.NoError(e.T(), err)
	require.Empty(e.T(), maxTTLValue)
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingDefaultValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(7776000000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(token.Status.ExpiresAt, 7776000000))
}

func (e *ExtTokenTTLTestSuite) TestMaxOfZeroAllowsDefaultTTLToApply() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a max of 0 still allows the configured default TTL to apply, rather than making the token unlimited")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(86400000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 24*time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestExplicitTTLOfZero() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl of 0 is treated the same as omitting spec.ttl")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(86400000), token.Spec.TTL)
	require.NotEqual(e.T(), int64(0), token.Spec.TTL)
	require.NotEqual(e.T(), int64(-1), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 24*time.Hour))

	omittedToken, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), token.Spec.TTL, omittedToken.Spec.TTL)
}

func (e *ExtTokenTTLTestSuite) TestChangingDefaultDoesNotModifyExistingToken() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing the default TTL setting does not affect an already-created ext token")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	existingToken, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	existingTTL := existingToken.Spec.TTL
	existingExpiresAt := existingToken.Status.ExpiresAt
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(4320, 10)))

	refetchedToken, err := exttokenapi.GetExtTokenByName(e.client, existingToken.Name)
	require.NoError(e.T(), err)
	require.Equal(e.T(), existingTTL, refetchedToken.Spec.TTL)
	require.Equal(e.T(), existingExpiresAt, refetchedToken.Status.ExpiresAt)
	require.NoError(e.T(), tokenapi.VerifyTokenAuthenticates(existingToken.Status.BearerToken))

	newToken, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), tokenapi.MinutesToMilliseconds(4320), newToken.Spec.TTL)
}

func (e *ExtTokenTTLTestSuite) TestChangingMaxDoesNotModifyExistingToken() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing the max TTL setting does not affect an already-created ext token")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	existingToken, err := tokenapi.CreateExtToken(e.client, tokenapi.MinutesToMilliseconds(4320))
	require.NoError(e.T(), err)

	existingTTL := existingToken.Spec.TTL
	existingExpiresAt := existingToken.Status.ExpiresAt
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "1440"))

	refetchedToken, err := exttokenapi.GetExtTokenByName(e.client, existingToken.Name)
	require.NoError(e.T(), err)
	require.Equal(e.T(), existingTTL, refetchedToken.Spec.TTL)
	require.Equal(e.T(), existingExpiresAt, refetchedToken.Status.ExpiresAt)
	require.NoError(e.T(), tokenapi.VerifyTokenAuthenticates(existingToken.Status.BearerToken))

	newToken, err := tokenapi.CreateExtToken(e.client, tokenapi.MinutesToMilliseconds(4320))
	require.NoError(e.T(), err)
	require.Equal(e.T(), tokenapi.MinutesToMilliseconds(1440), newToken.Spec.TTL)
}

func (e *ExtTokenTTLTestSuite) TestNegativeDefaultValue() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying how a negative default TTL setting behaves, with and without a max TTL configured")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(-1, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "-1"))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	token, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.NoError(e.T(), tokenapi.VerifyNoExpiry(token.Status.ExpiresAt))
	require.Equal(e.T(), int64(-60000), token.Spec.TTL)
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	cappedToken, err := tokenapi.CreateExtToken(e.client, 0)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), cappedToken.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(cappedToken.Status.ExpiresAt, 604800000))
}

func (e *ExtTokenTTLTestSuite) TestExplicitTTLBelowMaxRespectedWhenDefaultExceedsMax() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl below the max is respected even when the default exceeds the max")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(20160, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "20160"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(e.client, 3600000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(3600000), token.Spec.TTL)
	require.NotEqual(e.T(), int64(604800000), token.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, time.Hour))
}

func (e *ExtTokenTTLTestSuite) TestTokenTTLOneMillisecondAboveMax() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a spec.ttl one millisecond above the max is capped at the max")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(e.T(), settingsapi.VerifyGlobalSettingValue(e.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	overToken, err := tokenapi.CreateExtToken(e.client, 604800001)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), overToken.Spec.TTL)
	require.NoError(e.T(), tokenapi.VerifyExpiresAtMatchesTTL(overToken.Status.ExpiresAt, 7*24*time.Hour))
	require.NoError(e.T(), tokenapi.VerifyTokenAuthenticates(overToken.Status.BearerToken))

	exactToken, err := tokenapi.CreateExtToken(e.client, 604800000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), exactToken.Spec.TTL)
}

func (e *ExtTokenTTLTestSuite) TestUpdatingTokenAppliesCurrentLimits() {
	subSession := e.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that updating an existing ext token re-applies the currently configured TTL limits")

	require.NoError(e.T(), settingsapi.ResetTokenTTLSettingsToDefault(e.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))

	_, standardUserClient, err := rbac.SetupUser(e.client, rbac.StandardUser.String())
	require.NoError(e.T(), err)

	token, err := tokenapi.CreateExtTokenOfKind(standardUserClient, "", 1209600000)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(1209600000), token.Spec.TTL)
	require.NoError(e.T(), extsettingsapi.UpdateGlobalSetting(e.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	token.Spec.Description = "updated description"
	updatedToken, err := exttokenapi.UpdateExtToken(standardUserClient, token)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), updatedToken.Spec.TTL)

	updatedToken.Spec.TTL = 1209600000
	reUpdatedToken, err := exttokenapi.UpdateExtToken(standardUserClient, updatedToken)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(604800000), reUpdatedToken.Spec.TTL)
	require.NotEqual(e.T(), int64(1209600000), reUpdatedToken.Spec.TTL)
	updatedToken = reUpdatedToken

	updatedToken.Spec.TTL = 3600000
	finalToken, err := exttokenapi.UpdateExtToken(standardUserClient, updatedToken)
	require.NoError(e.T(), err)
	require.Equal(e.T(), int64(3600000), finalToken.Spec.TTL)
}

func TestExtTokenTTLTestSuite(t *testing.T) {
	suite.Run(t, new(ExtTokenTTLTestSuite))
}

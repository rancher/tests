//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package ttl

import (
	"strconv"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	"github.com/rancher/shepherd/pkg/session"
	settingsapi "github.com/rancher/tests/actions/kubeapi/settings"
	tokenapi "github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	v3tokenapi "github.com/rancher/tests/actions/tokens"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type V3TokenTTLTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (v *V3TokenTTLTestSuite) TearDownSuite() {
	v.session.Cleanup()
}

func (v *V3TokenTTLTestSuite) SetupSuite() {
	v.session = session.NewSession()

	client, err := rancher.NewClient("", v.session)
	require.NoError(v.T(), err)
	v.client = client
}

func (v *V3TokenTTLTestSuite) TestV3DefaultTTLAppliesToNewToken() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a v3 token created without a TTL receives the configured default TTL")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(86400000), token.TTLMillis)
	require.NotEqual(v.T(), int64(604800000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3ExplicitTTLBelowMaxRespected() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit TTL below the max is respected rather than replaced by the default")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 3600000)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(3600000), token.TTLMillis)
	require.NotEqual(v.T(), int64(86400000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3ExplicitTTLExceedingMaxCapped() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit TTL above the max is capped at the max")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 1209600000)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(604800000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3DefaultTTLGreaterThanMaxReducedToMax() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default TTL greater than the max is reduced to the max")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(20160, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "20160"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(604800000), token.TTLMillis)
	require.NotEqual(v.T(), int64(1209600000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3DefaultTTLOfZeroUsesMax() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default TTL of 0 resolves to the configured max")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "0"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(604800000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3MaxOfZeroAllowsTokenLongerThanDefault() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a max of 0 allows an explicit TTL longer than the default")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	token, err := v3tokenapi.CreateV3Token(v.client, 2592000000)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(2592000000), token.TTLMillis)
	require.NotEqual(v.T(), int64(86400000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3ExplicitTTLOfZero() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit TTL of 0 is treated the same as omitting the TTL")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	createdToken, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)

	token, err := v3tokenapi.WaitForV3TokenExpiresAt(v.client, createdToken.ID)
	require.NoError(v.T(), err)

	require.Equal(v.T(), int64(86400000), token.TTLMillis)
	require.NotEqual(v.T(), int64(0), token.TTLMillis)
	require.NoError(v.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.ExpiresAt, 24*time.Hour))
}

func (v *V3TokenTTLTestSuite) TestV3TokenTTLOneMillisecondAboveMax() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a TTL one millisecond above the max is capped at the max")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	createdOverToken, err := v3tokenapi.CreateV3Token(v.client, 604800001)
	require.NoError(v.T(), err)

	overToken, err := v3tokenapi.WaitForV3TokenExpiresAt(v.client, createdOverToken.ID)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(604800000), overToken.TTLMillis)
	require.NoError(v.T(), tokenapi.VerifyExpiresAtMatchesTTL(overToken.ExpiresAt, 7*24*time.Hour))
	require.NoError(v.T(), tokenapi.VerifyTokenAuthenticates(overToken.Token))

	createdExactToken, err := v3tokenapi.CreateV3Token(v.client, 604800000)
	require.NoError(v.T(), err)

	exactToken, err := v3tokenapi.WaitForV3TokenExpiresAt(v.client, createdExactToken.ID)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(604800000), exactToken.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3DefaultAndMaxBothZeroAllowTokenWithoutExpiration() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default and max of 0 together produce a non-expiring v3 token that still authenticates")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "0"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	token, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(0), token.TTLMillis)
	require.Empty(v.T(), token.ExpiresAt)
	require.NoError(v.T(), tokenapi.VerifyTokenAuthenticates(token.Token))
}

func (v *V3TokenTTLTestSuite) TestV3ExplicitTTLEqualToMaxAccepted() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit TTL equal to the max is accepted unchanged")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 604800000)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(604800000), token.TTLMillis)
}

func (v *V3TokenTTLTestSuite) TestV3MaxOfZeroAllowsDefaultTTLToApply() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a max of 0 still allows the configured default TTL to apply, rather than making the token unlimited")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	createdToken, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)

	token, err := v3tokenapi.WaitForV3TokenExpiresAt(v.client, createdToken.ID)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(86400000), token.TTLMillis)
	require.NotEqual(v.T(), int64(0), token.TTLMillis)
	require.NoError(v.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.ExpiresAt, 24*time.Hour))
}

func (v *V3TokenTTLTestSuite) TestV3InstallationDefaultTTL() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a v3 token created at the installation default TTL settings receives the expected default TTL")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	defaultTTLValue, err := extsettingsapi.GetGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes)
	require.NoError(v.T(), err)
	require.Empty(v.T(), defaultTTLValue)
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingDefaultValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "129600"))

	maxTTLValue, err := extsettingsapi.GetGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes)
	require.NoError(v.T(), err)
	require.Empty(v.T(), maxTTLValue)
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingDefaultValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))

	createdToken, err := v3tokenapi.CreateV3Token(v.client, 0)
	require.NoError(v.T(), err)

	token, err := v3tokenapi.WaitForV3TokenExpiresAt(v.client, createdToken.ID)
	require.NoError(v.T(), err)

	require.Equal(v.T(), int64(7776000000), token.TTLMillis)
	require.NoError(v.T(), tokenapi.VerifyExpiresAtMatchesTTLMillis(token.ExpiresAt, 7776000000))
}

func (v *V3TokenTTLTestSuite) TestV3ExplicitTTLBelowMaxRespectedWhenDefaultExceedsMax() {
	subSession := v.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit TTL below the max is respected even when the default exceeds the max")

	require.NoError(v.T(), settingsapi.ResetTokenTTLSettingsToDefault(v.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(20160, 10)))
	require.NoError(v.T(), extsettingsapi.UpdateGlobalSetting(v.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenDefaultTTLMinutes, "20160"))
	require.NoError(v.T(), settingsapi.VerifyGlobalSettingValue(v.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(v.client, 3600000)
	require.NoError(v.T(), err)
	require.Equal(v.T(), int64(3600000), token.TTLMillis)
	require.NotEqual(v.T(), int64(1209600000), token.TTLMillis)
	require.NotEqual(v.T(), int64(604800000), token.TTLMillis)
}

func TestV3TokenTTLTestSuite(t *testing.T) {
	suite.Run(t, new(V3TokenTTLTestSuite))
}

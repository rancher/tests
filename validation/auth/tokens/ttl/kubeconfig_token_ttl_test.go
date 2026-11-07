//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package ttl

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	extsettingsapi "github.com/rancher/shepherd/extensions/kubeapi/settings"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	v3kubeconfig "github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigapi "github.com/rancher/tests/actions/kubeapi/kubeconfigs"
	settingsapi "github.com/rancher/tests/actions/kubeapi/settings"
	tokenapi "github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/rbac"
	v3tokenapi "github.com/rancher/tests/actions/tokens"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
)

type KubeconfigTokenTTLTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cluster *management.Cluster
}

func (kc *KubeconfigTokenTTLTestSuite) TearDownSuite() {
	kc.session.Cleanup()
}

func (kc *KubeconfigTokenTTLTestSuite) SetupSuite() {
	kc.session = session.NewSession()

	client, err := rancher.NewClient("", kc.session)
	require.NoError(kc.T(), err)
	kc.client = client
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigDefaultTTLAppliesToNewToken() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a kubeconfig token created without spec.ttl receives the configured default TTL")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "43200"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), backingToken.Spec.TTL)
	require.NotEqual(kc.T(), int64(2592000000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigExplicitTTLBelowMaxRespected() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl below the max is respected rather than replaced by the default")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	ttlSeconds := int64(3600)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(3600), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(3600000), backingToken.Spec.TTL)
	require.NotEqual(kc.T(), int64(86400000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestExplicitTTLExceedingMaxRejected() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl above the max is rejected")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	kubeconfigCountBefore, tokenCountBefore, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)

	ttlSeconds := int64(1209600)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)

	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(kc.T(), err.Error(), "spec.ttl 1209600 exceeds max ttl 604800")

	kubeconfigCountAfter, tokenCountAfter, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")
}

func (kc *KubeconfigTokenTTLTestSuite) TestDefaultGreaterThanMaxReducedToMax() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default TTL greater than the max is reduced to the max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(20160, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "20160"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(604800), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), backingToken.Spec.TTL)
	require.NotEqual(kc.T(), int64(1209600000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 7*24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestDefaultOfZeroUsesMax() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a default TTL of 0 resolves to the configured max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(604800), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 7*24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigExplicitTTLEqualToMaxAccepted() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl equal to the max is accepted")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	ttlSeconds := int64(604800)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)

	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 7*24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigExplicitTTLBelowMaxRespectedWhenDefaultExceedsMax() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl below the max is respected even when the default exceeds the max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(20160, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "20160"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	ttlSeconds := int64(3600)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(3600), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(3600000), backingToken.Spec.TTL)
	require.NotEqual(kc.T(), int64(604800000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigMaxOfZeroAllowsDefaultTTLToApply() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a max of 0 still allows the configured default TTL to apply, rather than making the token unlimited")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(129600, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(86400), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigExplicitTTLOfZero() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an explicit spec.ttl of 0 is treated the same as omitting spec.ttl")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(129600, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))

	ttlSeconds := int64(0)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(86400), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 24*time.Hour))

	omittedKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfig.Spec.TTL, omittedKubeconfig.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestInstallationDefaultTTL() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a kubeconfig token created at the installation default TTL settings receives the expected default TTL")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	kubeconfigDefaultValue, err := extsettingsapi.GetGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes)
	require.NoError(kc.T(), err)
	require.Empty(kc.T(), kubeconfigDefaultValue)
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingDefaultValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "43200"))

	kubeconfigMaxValue, err := extsettingsapi.GetGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes)
	require.NoError(kc.T(), err)
	require.Empty(kc.T(), kubeconfigMaxValue)
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingDefaultValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "129600"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(2592000), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(2592000000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 30*24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestNegativeTTLRejected() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a negative spec.ttl is rejected")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	kubeconfigCountBefore, tokenCountBefore, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)

	ttlSeconds := int64(-1)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(kc.T(), strings.ToLower(err.Error()), "negative")

	kubeconfigCountAfter, tokenCountAfter, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")
}

func (kc *KubeconfigTokenTTLTestSuite) TestChangingDefaultAndMaxTogetherLeavesExistingTokenUnchanged() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing the kubeconfig default and max together does not affect an already-created kubeconfig token")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	ttlSecondsA := int64(259200)
	kubeconfigA, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsA)
	require.NoError(kc.T(), err)
	backingTokenA, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigA.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(259200000), backingTokenA.Spec.TTL)
	recordedExpiresAt := backingTokenA.Status.ExpiresAt

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(60, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(1440, 10)))

	refetchedTokenA, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigA.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), recordedExpiresAt, refetchedTokenA.Status.ExpiresAt)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(refetchedTokenA.Status.BearerToken))

	kubeconfigB, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(3600), kubeconfigB.Spec.TTL)
	backingTokenB, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigB.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(3600000), backingTokenB.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestChangingDefaultDoesNotAffectExistingToken() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing the kubeconfig default TTL does not affect an already-created kubeconfig token")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	kubeconfigA, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	backingTokenA, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigA.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), backingTokenA.Spec.TTL)
	recordedExpiresAt := backingTokenA.Status.ExpiresAt

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(4320, 10)))

	refetchedTokenA, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigA.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), recordedExpiresAt, refetchedTokenA.Status.ExpiresAt)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(refetchedTokenA.Status.BearerToken))

	kubeconfigB, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(259200), kubeconfigB.Spec.TTL)
	backingTokenB, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigB.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(259200000), backingTokenB.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestChangingMaxDoesNotModifyExistingTokens() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing the kubeconfig max TTL does not affect an already-created kubeconfig token")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	ttlSecondsA := int64(259200)
	kubeconfigA, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsA)
	require.NoError(kc.T(), err)
	backingTokenA, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigA.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(259200000), backingTokenA.Spec.TTL)
	recordedExpiresAt := backingTokenA.Status.ExpiresAt

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(1440, 10)))

	refetchedTokenA, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfigA.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), recordedExpiresAt, refetchedTokenA.Status.ExpiresAt)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(refetchedTokenA.Status.BearerToken))

	ttlSecondsReject := int64(259200)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(kc.T(), err.Error(), "exceeds max ttl 86400")

	ttlSecondsAccept := int64(86400)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigUpdatingTokenAppliesCurrentLimits() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that updating an existing kubeconfig-kind ext token re-applies the currently configured kubeconfig TTL limits")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))

	_, standardUserClient, err := rbac.SetupUser(kc.client, rbac.StandardUser.String())
	require.NoError(kc.T(), err)
	token, err := tokenapi.CreateExtTokenOfKind(standardUserClient, tokenapi.ExtTokenKindKubeconfig, 1209600000)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(1209600000), token.Spec.TTL)

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	token.Spec.Description = "updated description"
	updatedToken, err := tokenapi.WaitForExtTokenUpdateTTL(standardUserClient, token, int64(604800000))
	require.NoError(kc.T(), err, "expected the updated token's TTL to converge to the new 7-day kubeconfig max")
	require.Equal(kc.T(), int64(604800000), updatedToken.Spec.TTL)

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(1440, 10)))

	updatedToken.Spec.Description = "updated description again"
	reUpdatedToken, err := tokenapi.WaitForExtTokenUpdateTTL(standardUserClient, updatedToken, int64(86400000))
	require.NoError(kc.T(), err, "expected the updated token's TTL to converge to the new 1-day auth-token max")
	require.Equal(kc.T(), int64(86400000), reUpdatedToken.Spec.TTL)

	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(reUpdatedToken.Status.BearerToken))

	reUpdatedToken.Spec.Kind = ""
	_, err = exttokenapi.UpdateExtToken(standardUserClient, reUpdatedToken)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "spec.kind is immutable")
}

func (kc *KubeconfigTokenTTLTestSuite) TestGeneralDefaultDoesNotAffectKubeconfigDefaultTTL() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the general default TTL setting does not affect the kubeconfig default TTL")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(4320, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "4320"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)

	require.Equal(kc.T(), int64(259200), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(259200000), backingToken.Spec.TTL)
	require.NotEqual(kc.T(), int64(86400000), backingToken.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestGeneralDefaultIndependentOfKubeconfigDefaultForExtTokens() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the general default TTL is applied independently of the kubeconfig default for general ext tokens")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(4320, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "4320"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtToken(kc.client, 0)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), token.Spec.TTL)
	require.NotEqual(kc.T(), int64(259200000), token.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestGeneralDefaultIndependentOfKubeconfigDefaultForV3Tokens() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the general default TTL is applied independently of the kubeconfig default for v3 tokens")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(4320, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "4320"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	token, err := v3tokenapi.CreateV3Token(kc.client, 0)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), token.TTLMillis)
	require.NotEqual(kc.T(), int64(259200000), token.TTLMillis)
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigMaxDoesNotLimitGeneralTokens() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the kubeconfig max TTL does not limit general (non-kubeconfig) tokens")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(60, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "60"))

	token, err := tokenapi.CreateExtToken(kc.client, tokenapi.MinutesToMilliseconds(4320))
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(259200000), token.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(token.Status.ExpiresAt, 3*24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestExistingGeneralTokenExpirationNotAffectedByKubeconfigMaxChanges() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing the kubeconfig max TTL does not affect an already-created general token's expiration")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(43200, 10)))

	token, err := tokenapi.CreateExtToken(kc.client, tokenapi.MinutesToMilliseconds(20160))
	require.NoError(kc.T(), err)
	recordedExpiresAt := token.Status.ExpiresAt

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(1440, 10)))

	refetchedToken, err := exttokenapi.GetExtTokenByName(kc.client, token.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), recordedExpiresAt, refetchedToken.Status.ExpiresAt)

	newToken, err := tokenapi.CreateExtToken(kc.client, tokenapi.MinutesToMilliseconds(20160))
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), newToken.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigMaxAboveGeneralMaxIsCutToGeneralMax() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a kubeconfig max above the general max is effectively cut to the general max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(129600, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(259200, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "43200"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "259200"))

	kubeconfigCountBefore, tokenCountBefore, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	ttlSecondsReject := int64(15552000)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "exceeds max ttl 7776000")

	kubeconfigCountAfter, tokenCountAfter, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")

	ttlSecondsAccept := int64(7776000)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(7776000), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(7776000000), backingToken.Spec.TTL)

	uiKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	uiToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, uiKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.LessOrEqual(kc.T(), uiToken.Spec.TTL, int64(7776000000))
}

func (kc *KubeconfigTokenTTLTestSuite) TestEffectiveKubeconfigMaxWhenKubeconfigMaxIsLarger() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying the effective kubeconfig max when the kubeconfig-specific max is the larger of the two")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "43200"))

	kubeconfigCountBefore, tokenCountBefore, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)

	ttlSecondsReject := int64(1209600)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "spec.ttl 1209600 exceeds max ttl 604800")
	require.NotContains(kc.T(), err.Error(), "2592000")

	kubeconfigCountAfter, tokenCountAfter, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")

	ttlSecondsOver := int64(604801)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsOver)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "exceeds max ttl 604800")

	ttlSecondsAccept := int64(604800)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), backingToken.Spec.TTL)

	uiKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	uiToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, uiKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.LessOrEqual(kc.T(), uiToken.Spec.TTL, int64(604800000))
}

func (kc *KubeconfigTokenTTLTestSuite) TestEffectiveKubeconfigMaxWhenGeneralMaxIsLarger() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying the effective kubeconfig max when the general max is the larger of the two")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "43200"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	ttlSecondsReject := int64(1209600)
	_, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "spec.ttl 1209600 exceeds max ttl 604800")

	ttlSecondsAccept := int64(604800)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), backingToken.Spec.TTL)

	generalToken, err := tokenapi.CreateExtToken(kc.client, 1209600000)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(1209600000), generalToken.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigMaxOfZeroFallsBackToGeneralMax() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a kubeconfig max of 0 falls back to the general max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "0"))

	ttlSecondsReject := int64(31536000)
	_, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "spec.ttl 31536000 exceeds max ttl 604800")

	ttlSecondsAccept := int64(604800)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 7*24*time.Hour))
}

func (kc *KubeconfigTokenTTLTestSuite) TestGeneralMaxOfZeroLeavesKubeconfigMaxAsTheLimit() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a general max of 0 leaves the kubeconfig max as the effective limit")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	ttlSecondsReject := int64(1209600)
	_, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "spec.ttl 1209600 exceeds max ttl 604800")

	ttlSecondsAccept := int64(604800)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigMaxOfZeroWithGeneralMaxOfZeroAllowsUnlimitedTTL() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a kubeconfig max and general max of 0 together allow an unlimited TTL")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	ttlSeconds := int64(31536000)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)

	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(31536000000), backingToken.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestGeneralMaxLimitsKubeconfigTokens() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the general max TTL limits kubeconfig tokens when it is lower than the kubeconfig max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(60, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "60"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))

	ttlSecondsReject := int64(259200)
	_, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "spec.ttl 259200 exceeds max ttl 3600")

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(3600), kubeconfig.Spec.TTL)

	ttlSecondsAccept := int64(3600)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
}

func (kc *KubeconfigTokenTTLTestSuite) TestChangingASettingChangesEffectiveMaxForNewRequestsOnly() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that changing a max TTL setting changes the effective max for new requests only, leaving existing tokens unaffected")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(43200, 10)))

	ttlSeconds := int64(1209600)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	recordedExpiresAt := backingToken.Status.ExpiresAt

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	refetchedToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), recordedExpiresAt, refetchedToken.Status.ExpiresAt)
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "43200"))

	ttlSecondsReject := int64(1209600)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.Contains(kc.T(), err.Error(), "exceeds max ttl 604800")
}

func (kc *KubeconfigTokenTTLTestSuite) TestLoweringGeneralMaxDoesNotChangeExistingKubeconfigTokensButLimitsNewOnes() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that lowering the general max does not change existing kubeconfig tokens but limits new ones")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "43200"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "43200"))

	ttlSeconds := int64(1209600)
	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.NoError(kc.T(), err)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	recordedExpiresAt := backingToken.Status.ExpiresAt

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(1440, 10)))

	refetchedToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), recordedExpiresAt, refetchedToken.Status.ExpiresAt)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(refetchedToken.Status.BearerToken))

	ttlSecondsReject := int64(1209600)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsReject)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err))
	require.Contains(kc.T(), err.Error(), "spec.ttl 1209600 exceeds max ttl 86400")

	ttlSecondsAccept := int64(86400)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSecondsAccept)
	require.NoError(kc.T(), err)
}

func (kc *KubeconfigTokenTTLTestSuite) TestExtTokenOfKindKubeconfigCreatedByAUser() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an ext token of kind kubeconfig created directly by a user follows the kubeconfig TTL settings")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, strconv.FormatInt(4320, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "43200"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenDefaultTTLMinutes, "4320"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "10080"))

	token, err := tokenapi.CreateExtTokenOfKind(kc.client, tokenapi.ExtTokenKindKubeconfig, 0)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), token.Spec.TTL)

	cappedToken, err := tokenapi.CreateExtTokenOfKind(kc.client, tokenapi.ExtTokenKindKubeconfig, 1209600000)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(604800000), cappedToken.Spec.TTL)
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigDefaultAndMaxBothZeroGeneralMaxFinite() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that a kubeconfig default and max of 0 fall back to a finite general max")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(129600, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "129600"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(7776000), kubeconfig.Spec.TTL)

	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(7776000000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyExpiresAtMatchesTTL(backingToken.Status.ExpiresAt, 90*24*time.Hour))

	uiKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	uiToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, uiKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(7776000000), uiToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(uiToken.Status.BearerToken))
}

func (kc *KubeconfigTokenTTLTestSuite) TestKubeconfigDefaultMaxAndGeneralMaxAllZero() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying behavior when the kubeconfig default, kubeconfig max and general max are all 0")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(0, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(0, 10)))

	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "0"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "0"))

	kubeconfigCountBefore, tokenCountBefore, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)

	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	kubeconfigCountAfter, tokenCountAfter, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")

	clientConfig, err := v3kubeconfig.GetKubeconfig(kc.client, extclusterapi.LocalCluster)
	require.NoError(kc.T(), err)
	rawConfig, err := (*clientConfig).RawConfig()
	require.NoError(kc.T(), err)
	credential, err := kubeconfigapi.ExtractBearerToken(rawConfig)
	require.NoError(kc.T(), err)

	tokenID := strings.SplitN(credential, ":", 2)[0]
	v3Token, err := kc.client.Management.Token.ByID(tokenID)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(0), v3Token.TTLMillis)
	require.Empty(kc.T(), v3Token.ExpiresAt)

	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(credential))
}

func (kc *KubeconfigTokenTTLTestSuite) TestAllKubeconfigPathsUseTheSameDefaults() {
	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that the ext.Kubeconfig API path uses the same default TTL as the other kubeconfig creation paths")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.AuthTokenMaxTTLMinutes, strconv.FormatInt(43200, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, "1440"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, "10080"))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.AuthTokenMaxTTLMinutes, "43200"))

	kubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400), kubeconfig.Spec.TTL)
	backingToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, kubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), backingToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(backingToken.Status.BearerToken))

	uiKubeconfig, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", nil)
	require.NoError(kc.T(), err)
	uiToken, err := kubeconfigapi.GetFirstBackingExtToken(kc.client, uiKubeconfig.Name)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), int64(86400000), uiToken.Spec.TTL)
	require.NoError(kc.T(), tokenapi.VerifyTokenAuthenticates(uiToken.Status.BearerToken))
}

// Skipped Test - GH#58661
func (kc *KubeconfigTokenTTLTestSuite) TestIntegerOverflowInTTLValidationRejected() {
	kc.T().Skip("Blocked by https://github.com/rancher/rancher/issues/58661")

	subSession := kc.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Verifying that an oversized spec.ttl is rejected instead of overflowing int64 and bypassing the max ttl check")

	require.NoError(kc.T(), settingsapi.ResetTokenTTLSettingsToDefault(kc.client, []string{
		settingsapi.AuthTokenDefaultTTLMinutes,
		settingsapi.AuthTokenMaxTTLMinutes,
		settingsapi.KubeconfigDefaultTTLMinutes,
		settingsapi.KubeconfigMaxTTLMinutes,
	}))

	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), extsettingsapi.UpdateGlobalSetting(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigDefaultTTLMinutes, strconv.FormatInt(1440, 10)))
	require.NoError(kc.T(), settingsapi.VerifyGlobalSettingValue(kc.client, settingsapi.KubeconfigMaxTTLMinutes, strconv.FormatInt(10080, 10)))

	ttlSeconds := int64(1209600)
	_, err := kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &ttlSeconds)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(kc.T(), err.Error(), "spec.ttl 1209600 exceeds max ttl 604800")

	kubeconfigCountBefore, tokenCountBefore, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)

	overflowingTTLSeconds := int64(18446744073709552)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &overflowingTTLSeconds)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(kc.T(), strings.ToLower(err.Error()), "exceed")
	require.Contains(kc.T(), err.Error(), "604800")

	kubeconfigCountAfter, tokenCountAfter, err := kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")

	negativeOverflowingTTLSeconds := int64(9223372036854776)
	_, err = kubeconfigapi.CreateKubeconfig(kc.client, []string{extclusterapi.LocalCluster}, "", &negativeOverflowingTTLSeconds)
	require.Error(kc.T(), err)
	require.True(kc.T(), k8serrors.IsBadRequest(err), "expected HTTP 400, got: %s", err)
	require.Contains(kc.T(), strings.ToLower(err.Error()), "exceed")
	require.Contains(kc.T(), err.Error(), "604800")
	require.NotContains(kc.T(), err.Error(), "can't be negative (infinite)")

	kubeconfigCountAfter, tokenCountAfter, err = kubeconfigapi.FetchKubeconfigsAndBackingTokensCount(kc.client)
	require.NoError(kc.T(), err)
	require.Equal(kc.T(), kubeconfigCountBefore, kubeconfigCountAfter, "no new kubeconfig should have been created")
	require.Equal(kc.T(), tokenCountBefore, tokenCountAfter, "no new backing ext.Token should have been created")
}

func TestKubeconfigTokenTTLTestSuite(t *testing.T) {
	suite.Run(t, new(KubeconfigTokenTTLTestSuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12

package useractivity

import (
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/settings"
	"github.com/rancher/tests/actions/tokens/exttokens"
	"github.com/rancher/tests/actions/useractivity"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type UserActivityTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
}

func (ua *UserActivityTestSuite) TearDownSuite() {
	ua.session.Cleanup()
}

func (ua *UserActivityTestSuite) SetupSuite() {
	ua.session = session.NewSession()

	client, err := rancher.NewClient("", ua.session)
	require.NoError(ua.T(), err)
	ua.client = client
}

func (ua *UserActivityTestSuite) TestGetUserActivity() {
	subSession := ua.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create a session token for the user")
	createdExtSessionToken, err := exttokens.CreateExtSessionToken(ua.client)
	require.NoError(ua.T(), err)

	log.Info("Get the useractivity for the ext session token")
	extUserActivity, err := useractivity.GetUserActivity(ua.client, createdExtSessionToken.Name)
	require.NoError(ua.T(), err)

	log.Info("Verify useractivity and ext session token resourceVersion match")
	require.NotEmpty(ua.T(), extUserActivity.ResourceVersion)
	require.Equal(ua.T(), createdExtSessionToken.ResourceVersion, extUserActivity.ResourceVersion)
}

func (ua *UserActivityTestSuite) TestUserActivityIdleSessionTimeout() {
	subSession := ua.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Set auth-user-session-idle-ttl-minutes to 2 minutes")
	authUserSessionIdleTTLMinutes, err := ua.client.WranglerContext.Mgmt.Setting().Get(settings.AuthUserSessionIdleTTlMinutesSetting, metav1.GetOptions{})
	require.NoError(ua.T(), err)
	authUserSessionIdleTTLMinutes.Value = "2"
	_, err = ua.client.WranglerContext.Mgmt.Setting().Update(authUserSessionIdleTTLMinutes)
	require.NoError(ua.T(), err)
	require.Equal(ua.T(), "2", authUserSessionIdleTTLMinutes.Value, "Expected value to be 2")

	log.Info("Create ext session token")
	createdExtSessionToken, err := exttokens.CreateExtSessionToken(ua.client)
	require.NoError(ua.T(), err)

	log.Info("Get the useractivity for the ext session token")
	extUserActivity, err := useractivity.GetUserActivity(ua.client, createdExtSessionToken.Name)
	require.NoError(ua.T(), err)

	log.Info("Verify useractivity and ext session token resourceVersion match")
	require.NotEmpty(ua.T(), extUserActivity.ResourceVersion)
	require.Equal(ua.T(), createdExtSessionToken.ResourceVersion, extUserActivity.ResourceVersion)

	log.Info("Update the useractivity to trigger usage of ext session token")
	extUserActivityToUpdate := extUserActivity.DeepCopy()
	if extUserActivityToUpdate.ObjectMeta.Labels == nil {
		extUserActivityToUpdate.ObjectMeta.Labels = make(map[string]string)
	}
	extUserActivityToUpdate.ObjectMeta.Labels["foo"] = "bar"
	updatedUserActivity, err := useractivity.UpdateUserActivity(ua.client, extUserActivityToUpdate)
	require.NoError(ua.T(), err)
	require.NotNil(ua.T(), updatedUserActivity.Spec.SeenAt)

	log.Infof("Verify useractivity status.ExpiresAt has the correct value based on the current time + %s value", settings.AuthUserSessionIdleTTlMinutesSetting)
	idleTimeoutDuration := defaults.TwoMinuteTimeout
	expectedExpiration := time.Now().UTC().Add(idleTimeoutDuration)
	actualExpiresAt := updatedUserActivity.Status.ExpiresAt
	actualExpriesAtTime, err := time.Parse(time.RFC3339, actualExpiresAt)
	require.NoError(ua.T(), err, "Failed to parse expiresAt timestamp")
	require.NotNil(ua.T(), updatedUserActivity.Status.ExpiresAt)
	require.WithinDuration(ua.T(), expectedExpiration, actualExpriesAtTime, defaults.TenSecondTimeout)

	log.Info("Polling until session idle timeout expires...")
	err = useractivity.WaitForUserActivityError(ua.client, extUserActivityToUpdate.Name)
	require.Error(ua.T(), err, "Expected error to be thrown during polling")
	require.True(ua.T(), k8serrors.IsForbidden(err), "Expected a 'Forbidden' error, but got: %v", err)
	require.Contains(ua.T(), err.Error(), "session idle timeout expired")

	log.Info("Resetting auth-user-session-idle-ttl-minutes to default value")
	err = settings.ResetGlobalSettingToDefaultValue(ua.client, settings.AuthUserSessionIdleTTlMinutesSetting)
	require.NoError(ua.T(), err)
}

func (ua *UserActivityTestSuite) TestUserActivitySessionTokenExpired() {
	subSession := ua.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Set auth-token-max-ttl-minutes to 2 minutes")
	authTokenMaxTTLMinutes, err := ua.client.WranglerContext.Mgmt.Setting().Get(settings.AuthTokenMaxTTLMinutesSetting, metav1.GetOptions{})
	require.NoError(ua.T(), err)
	authTokenMaxTTLMinutes.Value = "2"
	updatedAuthTokenMaxTTLMinutes, err := ua.client.WranglerContext.Mgmt.Setting().Update(authTokenMaxTTLMinutes)
	require.NoError(ua.T(), err)
	require.Equal(ua.T(), "2", updatedAuthTokenMaxTTLMinutes.Value, "Expected value to be 2")

	log.Info("Create a ext session token for the user")
	createdExtSessionToken, err := exttokens.CreateExtSessionToken(ua.client)
	require.NoError(ua.T(), err)

	log.Info("Get the useractivity for the ext session token")
	extUserActivity, err := useractivity.GetUserActivity(ua.client, createdExtSessionToken.Name)
	require.NoError(ua.T(), err)

	log.Info("Verify useractivity and session token resourceVersion match")
	require.NotEmpty(ua.T(), extUserActivity.ResourceVersion)
	require.Equal(ua.T(), createdExtSessionToken.ResourceVersion, extUserActivity.ResourceVersion)

	log.Info("Polling until ext session token expires...")
	err = useractivity.WaitForUserActivityError(ua.client, createdExtSessionToken.Name)
	require.Error(ua.T(), err, "Expected error to be thrown during polling")
	require.True(ua.T(), k8serrors.IsForbidden(err), "Expected a 'Forbidden' error, but got: %v", err)
	require.Contains(ua.T(), err.Error(), "token is expired")

	log.Info("Resetting auth-token-max-ttl-minutes to default value")
	err = settings.ResetGlobalSettingToDefaultValue(ua.client, settings.AuthTokenMaxTTLMinutesSetting)
	require.NoError(ua.T(), err)
}

func (ua *UserActivityTestSuite) TestUpdateUserActivitySeenAtFieldToFutureDate() {
	subSession := ua.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext session token")
	createdExtSessionToken, err := exttokens.CreateExtSessionToken(ua.client)
	require.NoError(ua.T(), err)

	log.Info("Get the useractivity")
	extUserActivity, err := useractivity.GetUserActivity(ua.client, createdExtSessionToken.Name)
	require.NoError(ua.T(), err)

	log.Info("Update useractivity spec.seenAt field to future date")
	futureDate := time.Now().UTC().Add(defaults.TenMinuteTimeout)
	metaV1FutureDate := metav1.NewTime(futureDate)
	extUserActivity.Spec.SeenAt = &metaV1FutureDate
	updatedExtUserActivity, err := useractivity.UpdateUserActivity(ua.client, extUserActivity)
	require.NoError(ua.T(), err)

	log.Info("Verify useractivity spec.seenAt field set to current time instead of future date")
	currentTime := time.Now().UTC()
	require.NotEqual(ua.T(), currentTime, updatedExtUserActivity.Spec.SeenAt)
	require.WithinDuration(ua.T(), currentTime, updatedExtUserActivity.Spec.SeenAt.Time, defaults.TenSecondTimeout)
}

func (ua *UserActivityTestSuite) TestUpdateUserActivitySeenAtFieldToNil() {
	subSession := ua.session.NewSession()
	defer subSession.Cleanup()

	log.Info("Create ext session token")
	createdExtSessionToken, err := exttokens.CreateExtSessionToken(ua.client)
	require.NoError(ua.T(), err)

	log.Info("Get the useractivity")
	extUserActivity, err := useractivity.GetUserActivity(ua.client, createdExtSessionToken.Name)
	require.NoError(ua.T(), err)

	log.Info("Verify updating useractivity.spec.seenAt field to nil sets field to current time instead of nil")
	extUserActivity.Spec.SeenAt = nil
	updatedExtUserActivity, err := useractivity.UpdateUserActivity(ua.client, extUserActivity)
	require.NoError(ua.T(), err)
	require.NotNil(ua.T(), updatedExtUserActivity.Spec.SeenAt)
}

func TestUserActivityTestSuite(ua *testing.T) {
	suite.Run(ua, new(UserActivityTestSuite))
}

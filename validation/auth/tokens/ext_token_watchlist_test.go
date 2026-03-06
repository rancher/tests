//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package tokens

import (
	"context"
	"strconv"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/settings"
	exttokenapi "github.com/rancher/tests/actions/tokens/exttokens"
	watchlistapi "github.com/rancher/tests/actions/watchlist"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ExtTokenWatchListTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	cluster            *management.Cluster
	defaultExtTokenTTL int64
}

func (w *ExtTokenWatchListTestSuite) TearDownSuite() {
	w.session.Cleanup()
}

func (w *ExtTokenWatchListTestSuite) SetupSuite() {
	w.session = session.NewSession()

	client, err := rancher.NewClient("", w.session)
	require.NoError(w.T(), err)
	w.client = client

	log.Info("Getting default TTL value to be used in tests")
	defaultTTLString, err := settings.GetGlobalSettingDefaultValue(w.client, settings.AuthTokenMaxTTLMinutes)
	require.NoError(w.T(), err)
	defaultTTLInt, err := strconv.Atoi(defaultTTLString)
	require.NoError(w.T(), err)
	defaultTTL := int64(defaultTTLInt * 60000)
	w.defaultExtTokenTTL = defaultTTL
}

func (w *ExtTokenWatchListTestSuite) TestWatchListForExtTokens() {
	subSession := w.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating ext token", w.client.UserID)
	_, err := exttokenapi.CreateExtToken(w.client, w.defaultExtTokenTTL)
	require.NoError(w.T(), err)

	log.Info("Starting watch on ext tokens")
	sendInitial := true
	watcher, err := w.client.WranglerContext.Ext.Token().Watch(metav1.ListOptions{
		SendInitialEvents:    &sendInitial,
		AllowWatchBookmarks:  true,
		ResourceVersionMatch: metav1.ResourceVersionMatchNotOlderThan,
	})
	require.NoError(w.T(), err, "failed to start watch")

	log.Info("Verifying WatchList completion signal")
	ctx, cancel := context.WithTimeout(context.Background(), defaults.OneMinuteTimeout)
	defer cancel()

	err = watchlistapi.WaitForWatchListEnd(ctx, watcher)
	require.NoError(w.T(), err, "WatchList validation failed")
}

func TestExtTokenWatchListTestSuite(w *testing.T) {
	suite.Run(w, new(ExtTokenWatchListTestSuite))
}

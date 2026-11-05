//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

package tokens

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/extensions/kubeapi/secrets"
	exttokenapi "github.com/rancher/shepherd/extensions/kubeapi/tokens"
	"github.com/rancher/shepherd/pkg/session"
	kubeapiauthactions "github.com/rancher/tests/actions/auth/kubeapiauth"
	"github.com/rancher/tests/actions/kubeapi/auditlog"
	"github.com/rancher/tests/actions/kubeapi/rancherserver"
	"github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	tokenapi "github.com/rancher/tests/actions/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var lastUsedAtCategories = []tokenapi.Category{tokenapi.V3, tokenapi.Ext}

type TokenLastUsedAtTestSuite struct {
	suite.Suite
	client    *rancher.Client
	session   *session.Session
	requester *tokenapi.Requester
}

func (s *TokenLastUsedAtTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")

	s.client = client
	s.requester = tokenapi.NewRequester(client)
}

func (s *TokenLastUsedAtTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *TokenLastUsedAtTestSuite) TestFirstUseIsWrittenRightAway() {
	for _, category := range lastUsedAtCategories {
		s.Run(string(category), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := tokenapi.NewCredential(subClient, category)
			require.NoError(s.T(), err, "Failed to mint a %v token", category)

			unused, err := tokenapi.GetLastUsedAt(subClient, credential.Target())
			require.NoError(s.T(), err, "Failed to read the new %v token", category)

			assert.Nil(s.T(), unused, "A new %v token should hold no lastUsedAt", category)

			request, err := s.requester.Send(credential, tokenapi.ProxyPath)
			require.NoError(s.T(), err, "Failed to make a request with the %v token", category)

			assert.Equal(s.T(), http.StatusOK, request.StatusCode, "The %v token should be accepted", category)

			written, err := tokenapi.WaitForLastUsedAtChange(subClient, credential.Target(), unused)
			require.NoError(s.T(), err, "The first use of the %v token should be written", category)

			assert.NoError(s.T(), tokenapi.VerifyLastUsedAtNear(written, request.At),
				"The first use of a %v token should be written with the time of the request", category)
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestSteadyUseIsWrittenOnceAMinuteToTheExactSecond() {
	for _, category := range lastUsedAtCategories {
		s.Run(string(category), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, _, err := tokenapi.CreateUsedCredential(subClient, s.requester, category)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", category)

			s.T().Logf("Using the %v token every %v for %v", category, defaults.FiveHundredMillisecondTimeout/5, defaults.TwoMinuteTimeout)

			requests, writes, err := tokenapi.RecordWrites(subClient, s.requester, credential, tokenapi.ProxyPath, defaults.FiveHundredMillisecondTimeout/5, defaults.TwoMinuteTimeout)
			require.NoError(s.T(), err, "Failed to record the writes of the %v token", category)

			assert.Greater(s.T(), len(requests), 300, "The loop should be fast enough to write once a second without the throttle")
			assert.NoError(s.T(), tokenapi.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.NoError(s.T(), tokenapi.VerifyWritesPerMinute(writes, defaults.TwoMinuteTimeout), "A %v token in steady use should be written once a minute", category)
			assert.NoError(s.T(), tokenapi.VerifyWritesMatchRequests(writes, requests), "Each lastUsedAt should be the exact second of a request")
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestInfrequentUseIsWrittenOnEveryRequest() {
	for _, category := range lastUsedAtCategories {
		s.Run(string(category), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := tokenapi.NewCredential(subClient, category)
			require.NoError(s.T(), err, "Failed to mint a %v token", category)

			s.T().Logf("Using the %v token every %v for %v", category, defaults.NinetySecondTimeout, 3*defaults.NinetySecondTimeout)

			requests, writes, err := tokenapi.RecordWrites(subClient, s.requester, credential, tokenapi.ProxyPath, defaults.NinetySecondTimeout, 3*defaults.NinetySecondTimeout)
			require.NoError(s.T(), err, "Failed to record the writes of the %v token", category)

			assert.NoError(s.T(), tokenapi.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.NoError(s.T(), tokenapi.VerifyEveryRequestWasWritten(writes, requests), "Each request more than a minute after the last should be written")
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestConcurrentBurstsWriteLastUsedAtOnce() {
	modes := []struct {
		name   string
		warmed bool
	}{
		{"connections opened by the burst", false},
		{"connections opened first", true},
	}

	for _, category := range lastUsedAtCategories {
		for _, mode := range modes {
			s.Run(fmt.Sprintf("%v/%v", category, mode.name), func() {
				subSession := s.session.NewSession()
				defer subSession.Cleanup()

				subClient, err := s.client.WithSession(subSession)
				require.NoError(s.T(), err, "Failed to scope the client to the test session")

				credential, _, err := tokenapi.CreateUsedCredential(subClient, s.requester, category)
				require.NoError(s.T(), err, "Failed to mint and use a %v token", category)

				_, err = tokenapi.SetLastUsedAtAndWait(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
				require.NoError(s.T(), err, "Failed to age the lastUsedAt of the %v token", category)

				sent, writesPerBurst, err := tokenapi.RecordBursts(subClient, s.requester, credential, tokenapi.ProxyPath, 20, 3, mode.warmed)
				require.NoError(s.T(), err, "Failed to release the bursts")
				require.Len(s.T(), writesPerBurst, 3, "Every burst should have been counted")

				for burst, requests := range sent {
					assert.NoError(s.T(), tokenapi.VerifyAllSucceeded(requests), "Every request of burst %d should be accepted", burst+1)
				}

				assert.GreaterOrEqual(s.T(), writesPerBurst[0], 1, "The first burst after the minute expired should be written")
				assert.LessOrEqual(s.T(), writesPerBurst[0], 2, "The first burst should write once, or twice across a second boundary")

				for burst := 1; burst < 3; burst++ {
					assert.Zero(s.T(), writesPerBurst[burst], "Burst %d within the same minute should not write again", burst+1)
				}
			})
		}
	}
}

func (s *TokenLastUsedAtTestSuite) TestRequestsSpreadAcrossReplicasWriteOnceAMinute() {
	rancherPods, err := rancherserver.ListRancherServerPods(s.client)
	require.NoError(s.T(), err, "Failed to list the Rancher server pods")

	if len(rancherPods) < 2 {
		s.T().Skip(fmt.Sprintf("Rancher runs %d replica, and requests can only be spread across 2 or more", len(rancherPods)))
	}

	auditSession := s.session.NewSession()
	defer auditSession.Cleanup()

	auditClient, err := s.client.WithSession(auditSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	restore, err := auditlog.EnsureRancherAuditLogging(auditClient, auditlog.RancherAuditLevelHeaders)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on, which is how the test sees which replica served each request")
	auditSession.RegisterCleanupFunc(restore)

	spread := s.requester.WithFreshConnections()

	for _, category := range lastUsedAtCategories {
		s.Run(string(category), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, _, err := tokenapi.CreateUsedCredential(subClient, spread, category)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", category)

			s.T().Logf("Spreading requests of the %v token across %d replicas for %v", category, len(rancherPods), defaults.TwoMinuteTimeout)

			requests, writes, err := tokenapi.RecordWrites(subClient, spread, credential, tokenapi.ProxyPath, defaults.FiveHundredMillisecondTimeout, defaults.TwoMinuteTimeout)
			require.NoError(s.T(), err, "Failed to record the writes of the %v token", category)

			assert.NoError(s.T(), tokenapi.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.NoError(s.T(), tokenapi.VerifyWritesPerMinute(writes, defaults.TwoMinuteTimeout), "Replicas should not each write the token once a minute")
			assert.NoError(s.T(), tokenapi.VerifyWritesNeverMoveBack(writes), "A replica should not overwrite a newer lastUsedAt with an older one")

			servedBy, err := rancherserver.FindRancherServerPodsLogging(subClient, auditlog.RancherAuditLogContainer, tokenapi.RequestMarkerParameter+"="+credential.Name)
			require.NoError(s.T(), err, "Failed to read which Rancher replicas served the requests")

			if len(servedBy) < 2 {
				s.T().Skip(fmt.Sprintf("Every request of the %v token reached %d Rancher replica, so the race across replicas was not exercised", category, len(servedBy)))
			}
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestStoredLastUsedAtWithinAMinuteIsLeftAlone() {
	offsets := []struct {
		name   string
		offset time.Duration
	}{
		{"30s ahead", 30 * defaults.OneSecondTimeout},
		{"30s behind", -30 * defaults.OneSecondTimeout},
		{"50s ahead", 50 * defaults.OneSecondTimeout},
		{"50s behind", -50 * defaults.OneSecondTimeout},
	}

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	keys := []string{}
	credentials := []*tokenapi.Credential{}
	targets := []tokenapi.Target{}

	for _, category := range lastUsedAtCategories {
		for _, stored := range offsets {
			credential, err := tokenapi.NewCredential(subClient, category)
			require.NoError(s.T(), err, "Failed to mint a %v token", category)

			keys = append(keys, fmt.Sprintf("%v/%v", category, stored.name))
			credentials = append(credentials, credential)
			targets = append(targets, credential.Target())
		}
	}

	requestAt := time.Now().Add(3 * defaults.TenSecondTimeout)
	plantTimes := []time.Time{}

	for index := range targets {
		plantTimes = append(plantTimes, requestAt.Add(offsets[index%len(offsets)].offset))
	}

	planted, err := tokenapi.SetLastUsedAtAndWaitUntil(subClient, targets, plantTimes, requestAt)
	require.NoError(s.T(), err, "Failed to plant the stored values")

	requests, err := s.requester.SendAll(credentials, tokenapi.ProxyPath)
	require.NoError(s.T(), err, "Failed to make one request with each token")

	unchanged := tokenapi.VerifyLastUsedAtUnchanged(subClient, targets, planted)

	for index, key := range keys {
		s.Run(key, func() {
			assert.Equal(s.T(), http.StatusOK, requests[index].StatusCode, "The never used token should be accepted")
			assert.NoError(s.T(), unchanged[index], "A stored lastUsedAt within a minute of the request should be left alone")
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestStoredLastUsedAtBeyondAMinuteIsOverwritten() {
	farFuture := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	epoch := time.Unix(0, 0).UTC()

	stored := []struct {
		name     string
		offset   time.Duration
		absolute *time.Time
	}{
		{"90s ahead", 90 * defaults.OneSecondTimeout, nil},
		{"90s behind", -90 * defaults.OneSecondTimeout, nil},
		{"70s ahead", 70 * defaults.OneSecondTimeout, nil},
		{"70s behind", -70 * defaults.OneSecondTimeout, nil},
		{"year 2099", 0, &farFuture},
		{"the epoch", 0, &epoch},
	}

	baseline, err := rancherserver.FindRancherServerLogs(s.client, kubeapiauthactions.LastUsedAtWriteError)
	require.NoError(s.T(), err, "Failed to list the lastUsedAt errors Rancher had logged")

	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	keys := []string{}
	credentials := []*tokenapi.Credential{}
	targets := []tokenapi.Target{}

	for _, category := range lastUsedAtCategories {
		for _, value := range stored {
			credential, err := tokenapi.NewCredential(subClient, category)
			require.NoError(s.T(), err, "Failed to mint a %v token", category)

			keys = append(keys, fmt.Sprintf("%v/%v", category, value.name))
			credentials = append(credentials, credential)
			targets = append(targets, credential.Target())
		}
	}

	requestAt := time.Now().Add(3 * defaults.TenSecondTimeout)
	plantTimes := []time.Time{}

	for index := range targets {
		value := stored[index%len(stored)]

		plantedAt := requestAt.Add(value.offset)
		if value.absolute != nil {
			plantedAt = *value.absolute
		}

		plantTimes = append(plantTimes, plantedAt)
	}

	planted, err := tokenapi.SetLastUsedAtAndWaitUntil(subClient, targets, plantTimes, requestAt)
	require.NoError(s.T(), err, "Failed to plant the stored values")

	requests, err := s.requester.SendAll(credentials, tokenapi.ProxyPath)
	require.NoError(s.T(), err, "Failed to make one request with each token")

	for index, key := range keys {
		s.Run(key, func() {
			assert.Equal(s.T(), http.StatusOK, requests[index].StatusCode, "The token should be accepted")

			changed, err := tokenapi.WaitForLastUsedAtChange(subClient, targets[index], planted[index])
			require.NoError(s.T(), err, "A stored lastUsedAt a minute or more from the request should be overwritten")

			assert.NoError(s.T(), tokenapi.VerifyLastUsedAtNear(changed, requests[index].At), "lastUsedAt should move to the time of the request")
		})
	}

	require.NoError(s.T(), kubeapiauthactions.VerifyNoLastUsedAtErrors(s.client, baseline), "Rancher should log no failed write of lastUsedAt")
}

func (s *TokenLastUsedAtTestSuite) TestEveryRequestPathThrottlesLastUsedAt() {
	paths := []tokenapi.RequestPath{tokenapi.ProxyPath, tokenapi.BearerAPIPath, tokenapi.BasicAPIPath}

	for _, category := range lastUsedAtCategories {
		for _, path := range paths {
			s.Run(fmt.Sprintf("%v/%v", category, path), func() {
				subSession := s.session.NewSession()
				defer subSession.Cleanup()

				subClient, err := s.client.WithSession(subSession)
				require.NoError(s.T(), err, "Failed to scope the client to the test session")

				credential, err := tokenapi.NewCredential(subClient, category)
				require.NoError(s.T(), err, "Failed to mint a %v token", category)

				requests, writes, err := tokenapi.RecordWrites(subClient, s.requester, credential, path, defaults.OneSecondTimeout, 2*defaults.TenSecondTimeout)
				require.NoError(s.T(), err, "Failed to record the writes of the %v token", category)

				assert.Greater(s.T(), len(requests), 10, "The loop should make enough requests to write more than once without the throttle")
				assert.NoError(s.T(), tokenapi.VerifyAllSucceeded(requests), "Every request should be accepted")
				assert.Len(s.T(), writes, 1, "Only the first use over the %v path should be written", path)

				planted, err := tokenapi.SetLastUsedAtAndWait(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
				require.NoError(s.T(), err, "Failed to age the lastUsedAt of the %v token", category)

				request, err := s.requester.Send(credential, path)
				require.NoError(s.T(), err, "Failed to make a request over the %v path", path)

				changed, err := tokenapi.WaitForLastUsedAtChange(subClient, credential.Target(), planted)
				require.NoError(s.T(), err, "A use over the %v path more than a minute after the last should be written", path)

				assert.NoError(s.T(), tokenapi.VerifyLastUsedAtNear(changed, request.At), "lastUsedAt should move to the time of the request")
			})
		}
	}
}

func (s *TokenLastUsedAtTestSuite) TestEveryTokenKindThrottlesLastUsedAt() {
	mints := []struct {
		name string
		mint func(client *rancher.Client) (*tokenapi.Credential, error)
	}{
		{"v3 api key", func(client *rancher.Client) (*tokenapi.Credential, error) {
			return tokenapi.NewCredential(client, tokenapi.V3)
		}},
		{"v3 kubeconfig token", tokenapi.NewKubeconfigCredential},
		{"v3 session token", tokenapi.NewUserSessionCredential},
		{"ext token", func(client *rancher.Client) (*tokenapi.Credential, error) {
			return tokenapi.NewCredential(client, tokenapi.Ext)
		}},
	}

	for _, mint := range mints {
		s.Run(mint.name, func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := mint.mint(subClient)
			require.NoError(s.T(), err, "Failed to mint a %v", mint.name)

			_, err = tokenapi.SetLastUsedAtAndWait(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
			require.NoError(s.T(), err, "Failed to age the lastUsedAt of the %v", mint.name)

			requests, writes, err := tokenapi.RecordWrites(subClient, s.requester, credential, tokenapi.BearerAPIPath, defaults.OneSecondTimeout, 2*defaults.TenSecondTimeout)
			require.NoError(s.T(), err, "Failed to record the writes of the %v", mint.name)

			assert.NoError(s.T(), tokenapi.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.Len(s.T(), writes, 1, "Only the first use of a %v should be written", mint.name)
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestTokenAuthenticationIsRefusedAsBefore() {
	baseline, err := rancherserver.FindRancherServerLogs(s.client, kubeapiauthactions.LastUsedAtWriteError)
	require.NoError(s.T(), err, "Failed to list the lastUsedAt errors Rancher had logged")

	for _, category := range lastUsedAtCategories {
		s.Run(string(category), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			shortLived, err := tokenapi.NewShortLivedCredential(subClient, category, defaults.NinetySecondTimeout)
			require.NoError(s.T(), err, "Failed to mint a %v token that lives %v", category, defaults.NinetySecondTimeout)

			beforeExpiry, err := s.requester.Send(shortLived, tokenapi.ProxyPath)
			require.NoError(s.T(), err, "Failed to make a request with the short-lived %v token", category)
			require.Equal(s.T(), http.StatusOK, beforeExpiry.StatusCode, "The short-lived %v token should be accepted before it expires", category)

			valid, _, err := tokenapi.CreateUsedCredential(subClient, s.requester, category)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", category)

			tampered := &tokenapi.Credential{Category: valid.Category, Name: valid.Name, Bearer: valid.Bearer + "tampered"}

			request, err := s.requester.Send(tampered, tokenapi.ProxyPath)
			assert.NoError(s.T(), err, "Failed to make a request with the tampered token")
			assert.Equal(s.T(), http.StatusUnauthorized, request.StatusCode, "A %v token with a tampered secret should be refused", category)

			disabled, _, err := tokenapi.CreateUsedCredential(subClient, s.requester, category)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", category)

			assert.NoError(s.T(), tokenapi.SetCredentialEnabled(subClient, disabled, false), "Failed to disable the %v token", category)
			assert.NoError(s.T(), s.requester.WaitForStatus(disabled, tokenapi.ProxyPath, http.StatusUnauthorized, defaults.OneMinuteTimeout),
				"A disabled %v token should be refused", category)

			deleted, _, err := tokenapi.CreateUsedCredential(subClient, s.requester, category)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", category)

			assert.NoError(s.T(), tokenapi.DeleteCredential(subClient, deleted), "Failed to delete the %v token", category)
			assert.NoError(s.T(), s.requester.WaitForStatus(deleted, tokenapi.ProxyPath, http.StatusUnauthorized, defaults.OneMinuteTimeout),
				"A deleted %v token should be refused", category)

			assert.NoError(s.T(), s.requester.WaitForStatus(valid, tokenapi.ProxyPath, http.StatusOK, defaults.OneMinuteTimeout), "The untouched %v token should still be accepted", category)

			assert.NoError(s.T(), s.requester.WaitForStatus(shortLived, tokenapi.ProxyPath, http.StatusUnauthorized, 3*defaults.OneMinuteTimeout),
				"An expired %v token should be refused", category)
		})
	}

	require.NoError(s.T(), kubeapiauthactions.VerifyNoLastUsedAtErrors(s.client, baseline), "Rancher should log no failed write of lastUsedAt for valid tokens")
}

func (s *TokenLastUsedAtTestSuite) TestAuditLogRecordsEveryRequestDespiteTheThrottle() {
	auditSession := s.session.NewSession()
	defer auditSession.Cleanup()

	auditClient, err := s.client.WithSession(auditSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	restore, err := auditlog.EnsureRancherAuditLogging(auditClient, auditlog.RancherAuditLevelHeaders)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on")

	auditSession.RegisterCleanupFunc(restore)

	for _, category := range lastUsedAtCategories {
		s.Run(string(category), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := tokenapi.NewCredential(subClient, category)
			require.NoError(s.T(), err, "Failed to mint a %v token", category)

			requests, err := s.requester.Repeat(credential, tokenapi.ProxyPath, 3*defaults.OneSecondTimeout, 3*defaults.TenSecondTimeout)
			require.NoError(s.T(), err, "Failed to make the requests")

			marker := tokenapi.RequestMarkerParameter + "=" + credential.Name

			entries, err := auditlog.WaitForAuditLogEntryCount(subClient, marker, len(requests))
			require.NoError(s.T(), err, "The audit log should hold an entry for each of the %d requests", len(requests))

			distinct := map[string]struct{}{}
			for _, entry := range entries {
				distinct[entry.AuditID] = struct{}{}
			}

			assert.Len(s.T(), entries, len(requests), "Each request should have exactly one audit entry")
			assert.Len(s.T(), distinct, len(requests), "Each audit entry should have its own audit ID")
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestExtTokenSecretOnlyChangesLastUsedAt() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	credential, _, err := tokenapi.CreateUsedCredential(subClient, s.requester, tokenapi.Ext)
	require.NoError(s.T(), err, "Failed to mint and use an ext token")

	planted, err := tokenapi.SetLastUsedAtAndWait(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
	require.NoError(s.T(), err, "Failed to age the lastUsedAt of the ext token")

	tokenBefore, err := exttokenapi.GetExtTokenByName(subClient, credential.Name)
	require.NoError(s.T(), err, "Failed to read the ext token before the write")

	secretBefore, err := secrets.GetSecretByName(subClient, extclusterapi.LocalCluster, exttokens.ExtTokenSecretNamespace, credential.Name)
	require.NoError(s.T(), err, "Failed to read the secret before the write")

	request, err := s.requester.Send(credential, tokenapi.ProxyPath)
	require.NoError(s.T(), err, "Failed to make a request with the ext token")
	require.Equal(s.T(), http.StatusOK, request.StatusCode, "The ext token should be accepted")

	_, err = tokenapi.WaitForLastUsedAtChange(subClient, credential.Target(), planted)
	require.NoError(s.T(), err, "The use more than a minute after the last should be written")

	tokenAfter, err := exttokenapi.GetExtTokenByName(subClient, credential.Name)
	require.NoError(s.T(), err, "Failed to read the ext token after the write")

	secretAfter, err := secrets.GetSecretByName(subClient, extclusterapi.LocalCluster, exttokens.ExtTokenSecretNamespace, credential.Name)
	require.NoError(s.T(), err, "Failed to read the secret after the write")

	require.NoError(s.T(), kubeapiauthactions.VerifyOnlyExtLastUsedAtChanged(tokenBefore, tokenAfter, secretBefore, secretAfter),
		"A write of lastUsedAt should leave every other part of the ext token alone")

	after, err := s.requester.Send(credential, tokenapi.ProxyPath)
	require.NoError(s.T(), err, "The ext token should still authenticate after the write")
	require.Equal(s.T(), http.StatusOK, after.StatusCode, "The ext token should still be accepted after the write")
}

func (s *TokenLastUsedAtTestSuite) TestManyTokensAreWrittenOncePerMinuteEach() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	credentials := []*tokenapi.Credential{}
	names := map[tokenapi.Category][]string{}

	for _, category := range lastUsedAtCategories {
		for i := 0; i < 25; i++ {
			credential, err := tokenapi.NewCredential(subClient, category)
			require.NoError(s.T(), err, "Failed to mint %v token %d", category, i+1)

			credentials = append(credentials, credential)
			names[category] = append(names[category], credential.Name)
		}
	}

	recorders := map[tokenapi.Category]*tokenapi.Recorder{}

	for _, category := range lastUsedAtCategories {
		recorder, err := tokenapi.RecordLastUsedAt(subClient, category, "", names[category]...)
		require.NoError(s.T(), err, "Failed to start recording the lastUsedAt of the %v tokens", category)

		recorders[category] = recorder
	}

	s.T().Logf("Polling %d tokens every %v for %v", len(credentials), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout)

	results, requestErr := tokenapi.RepeatAll(s.requester, credentials, tokenapi.ProxyPath, defaults.FiveSecondTimeout, defaults.OneMinuteTimeout)

	writes := map[string][]time.Time{}

	for category, recorder := range recorders {
		stopped, err := recorder.StopWhenQuiet(defaults.FiveSecondTimeout)
		require.NoError(s.T(), err, "The %v lastUsedAt should be read for the whole poll", category)

		for name, written := range stopped {
			writes[name] = written
		}
	}

	require.NoError(s.T(), requestErr, "Every poll should be accepted")

	for i, credential := range credentials {
		require.NoError(s.T(), tokenapi.VerifyAllSucceeded(results[i]), "Every request of %v token %v should be accepted", credential.Category, credential.Name)
		require.NoError(s.T(), tokenapi.VerifyWritesPerMinute(writes[credential.Name], defaults.OneMinuteTimeout),
			"%v token %v should be written once a minute", credential.Category, credential.Name)
	}
}

func TestTokenLastUsedAtTestSuite(t *testing.T) {
	suite.Run(t, new(TokenLastUsedAtTestSuite))
}

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
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/kubeapi/auditlog"
	"github.com/rancher/tests/actions/kubeapi/rancherserver"
	"github.com/rancher/tests/actions/kubeapi/tokens"
	"github.com/rancher/tests/actions/rbac"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

var lastUsedAtKinds = []tokens.Kind{tokens.V3, tokens.Ext}

type TokenLastUsedAtTestSuite struct {
	suite.Suite
	client    *rancher.Client
	session   *session.Session
	requester *authactions.Requester
}

func (s *TokenLastUsedAtTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err, "Failed to create Rancher client")

	s.client = client
	s.requester = authactions.NewRequester(client)
}

func (s *TokenLastUsedAtTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *TokenLastUsedAtTestSuite) TestFirstUseIsWrittenRightAway() {
	for _, kind := range lastUsedAtKinds {
		s.Run(string(kind), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewCredential(subClient, kind)
			require.NoError(s.T(), err, "Failed to mint a %v token", kind)

			unused, err := tokens.GetLastUsedAt(subClient, credential.Target())
			require.NoError(s.T(), err, "Failed to read the new %v token", kind)

			assert.Nil(s.T(), unused.Time, "A new %v token should hold no lastUsedAt", kind)

			request, err := s.requester.Send(credential, authactions.ProxyPath)
			require.NoError(s.T(), err, "Failed to make a request with the %v token", kind)

			assert.Equal(s.T(), http.StatusOK, request.StatusCode, "The %v token should be accepted", kind)

			written, err := authactions.WaitForLastUsedAtChange(subClient, credential.Target(), unused)
			require.NoError(s.T(), err, "The first use of the %v token should be written", kind)

			assert.NoError(s.T(), authactions.VerifyLastUsedAtNear(written, request.At),
				"The first use of a %v token should be written with the time of the request", kind)
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestSteadyUseIsWrittenOnceAMinuteToTheExactSecond() {
	for _, kind := range lastUsedAtKinds {
		s.Run(string(kind), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

			log.Infof("Using the %v token every %v for %v", kind, (defaults.FiveHundredMillisecondTimeout / 5), defaults.TwoMinuteTimeout)

			requests, writes, err := authactions.RecordWrites(subClient, s.requester, credential, authactions.ProxyPath, (defaults.FiveHundredMillisecondTimeout / 5), defaults.TwoMinuteTimeout)
			require.NoError(s.T(), err, "Failed to record the writes of the %v token", kind)

			assert.Greater(s.T(), len(requests), 300, "The loop should be fast enough to write once a second without the throttle")
			assert.NoError(s.T(), authactions.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.NoError(s.T(), authactions.VerifyWritesPerMinute(writes, defaults.TwoMinuteTimeout), "A %v token in steady use should be written once a minute", kind)
			assert.NoError(s.T(), authactions.VerifyWritesMatchRequests(writes, requests), "Each lastUsedAt should be the exact second of a request")
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestInfrequentUseIsWrittenOnEveryRequest() {
	for _, kind := range lastUsedAtKinds {
		s.Run(string(kind), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewCredential(subClient, kind)
			require.NoError(s.T(), err, "Failed to mint a %v token", kind)

			log.Infof("Using the %v token every %v for %v", kind, defaults.NinetySecondTimeout, (3 * defaults.NinetySecondTimeout))

			requests, writes, err := authactions.RecordWrites(subClient, s.requester, credential, authactions.ProxyPath, defaults.NinetySecondTimeout, (3 * defaults.NinetySecondTimeout))
			require.NoError(s.T(), err, "Failed to record the writes of the %v token", kind)

			assert.NoError(s.T(), authactions.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.NoError(s.T(), authactions.VerifyEveryRequestWasWritten(writes, requests), "Each request more than a minute after the last should be written")
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

	for _, kind := range lastUsedAtKinds {
		for _, mode := range modes {
			s.Run(fmt.Sprintf("%v/%v", kind, mode.name), func() {
				subSession := s.session.NewSession()
				defer subSession.Cleanup()

				subClient, err := s.client.WithSession(subSession)
				require.NoError(s.T(), err, "Failed to scope the client to the test session")

				credential, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
				require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

				_, err = authactions.PlantLastUsedAt(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
				require.NoError(s.T(), err, "Failed to age the lastUsedAt of the %v token", kind)

				sent, writesPerBurst, err := authactions.RecordBursts(subClient, s.requester, credential, authactions.ProxyPath, 20, 3, mode.warmed)
				require.NoError(s.T(), err, "Failed to release the bursts")

				for burst, requests := range sent {
					assert.NoError(s.T(), authactions.VerifyAllSucceeded(requests), "Every request of burst %d should be accepted", burst+1)
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

	spread := s.requester.WithFreshConnections()

	for _, kind := range lastUsedAtKinds {
		s.Run(string(kind), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, _, err := authactions.NewUsedCredential(subClient, spread, kind)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

			log.Infof("Spreading requests of the %v token across %d replicas for %v", kind, len(rancherPods), defaults.TwoMinuteTimeout)

			requests, writes, err := authactions.RecordWrites(subClient, spread, credential, authactions.ProxyPath, defaults.FiveHundredMillisecondTimeout, defaults.TwoMinuteTimeout)
			require.NoError(s.T(), err, "Failed to record the writes of the %v token", kind)

			assert.NoError(s.T(), authactions.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.NoError(s.T(), authactions.VerifyWritesPerMinute(writes, defaults.TwoMinuteTimeout), "Replicas should not each write the token once a minute")
			assert.NoError(s.T(), authactions.VerifyWritesNeverMoveBack(writes), "A replica should not overwrite a newer lastUsedAt with an older one")
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

	for _, kind := range lastUsedAtKinds {
		for _, stored := range offsets {
			s.Run(fmt.Sprintf("%v/%v", kind, stored.name), func() {
				subSession := s.session.NewSession()
				defer subSession.Cleanup()

				subClient, err := s.client.WithSession(subSession)
				require.NoError(s.T(), err, "Failed to scope the client to the test session")

				credential, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
				require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

				planted, err := authactions.PlantLastUsedAt(subClient, credential.Target(), time.Now().Add(defaults.FiveSecondTimeout+stored.offset))
				require.NoError(s.T(), err, "Failed to plant a lastUsedAt %v", stored.name)

				request, err := s.requester.Send(credential, authactions.ProxyPath)
				require.NoError(s.T(), err, "Failed to make a request with the %v token", kind)

				assert.Equal(s.T(), http.StatusOK, request.StatusCode, "The %v token should be accepted", kind)
				assert.NoError(s.T(), authactions.VerifyLastUsedAtUnchanged(subClient, credential.Target(), planted),
					"A lastUsedAt %v is within a minute of the request and should be left alone", stored.name)
			})
		}
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

	baseline, err := authactions.ListLastUsedAtErrors(s.client)
	require.NoError(s.T(), err, "Failed to list the lastUsedAt errors Rancher had logged")

	for _, kind := range lastUsedAtKinds {
		for _, value := range stored {
			s.Run(fmt.Sprintf("%v/%v", kind, value.name), func() {
				subSession := s.session.NewSession()
				defer subSession.Cleanup()

				subClient, err := s.client.WithSession(subSession)
				require.NoError(s.T(), err, "Failed to scope the client to the test session")

				credential, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
				require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

				plantedAt := time.Now().Add(defaults.FiveSecondTimeout + value.offset)
				if value.absolute != nil {
					plantedAt = *value.absolute
				}

				planted, err := authactions.PlantLastUsedAt(subClient, credential.Target(), plantedAt)
				require.NoError(s.T(), err, "Failed to plant a lastUsedAt of %v", value.name)

				request, err := s.requester.Send(credential, authactions.ProxyPath)
				require.NoError(s.T(), err, "Failed to make a request with the %v token", kind)

				assert.Equal(s.T(), http.StatusOK, request.StatusCode, "The %v token should be accepted", kind)

				changed, err := authactions.WaitForLastUsedAtChange(subClient, credential.Target(), planted)
				require.NoError(s.T(), err, "A lastUsedAt of %v is a minute or more from the request and should be overwritten", value.name)

				assert.NoError(s.T(), authactions.VerifyLastUsedAtNear(changed, request.At), "lastUsedAt should move to the time of the request")
			})
		}
	}

	require.NoError(s.T(), authactions.VerifyNoLastUsedAtErrors(s.client, baseline), "Rancher should log no failed write of lastUsedAt")
}

func (s *TokenLastUsedAtTestSuite) TestEveryRequestPathThrottlesLastUsedAt() {
	paths := []authactions.RequestPath{authactions.ProxyPath, authactions.BearerAPIPath, authactions.BasicAPIPath}

	for _, kind := range lastUsedAtKinds {
		for _, path := range paths {
			s.Run(fmt.Sprintf("%v/%v", kind, path), func() {
				subSession := s.session.NewSession()
				defer subSession.Cleanup()

				subClient, err := s.client.WithSession(subSession)
				require.NoError(s.T(), err, "Failed to scope the client to the test session")

				credential, err := authactions.NewCredential(subClient, kind)
				require.NoError(s.T(), err, "Failed to mint a %v token", kind)

				requests, writes, err := authactions.RecordWrites(subClient, s.requester, credential, path, defaults.OneSecondTimeout, (2 * defaults.TenSecondTimeout))
				require.NoError(s.T(), err, "Failed to record the writes of the %v token", kind)

				assert.Greater(s.T(), len(requests), 10, "The loop should make enough requests to write more than once without the throttle")
				assert.NoError(s.T(), authactions.VerifyAllSucceeded(requests), "Every request should be accepted")
				assert.Len(s.T(), writes, 1, "Only the first use over the %v path should be written", path)

				planted, err := authactions.PlantLastUsedAt(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
				require.NoError(s.T(), err, "Failed to age the lastUsedAt of the %v token", kind)

				request, err := s.requester.Send(credential, path)
				require.NoError(s.T(), err, "Failed to make a request over the %v path", path)

				changed, err := authactions.WaitForLastUsedAtChange(subClient, credential.Target(), planted)
				require.NoError(s.T(), err, "A use over the %v path more than a minute after the last should be written", path)

				assert.NoError(s.T(), authactions.VerifyLastUsedAtNear(changed, request.At), "lastUsedAt should move to the time of the request")
			})
		}
	}
}

func (s *TokenLastUsedAtTestSuite) TestEveryTokenKindThrottlesLastUsedAt() {
	mints := []struct {
		name string
		mint func(client *rancher.Client) (*authactions.Credential, error)
	}{
		{"v3 api key", authactions.NewV3Credential},
		{"v3 kubeconfig token", func(client *rancher.Client) (*authactions.Credential, error) {
			token, err := authactions.ClusterAuthTokenFromKubeconfig(client, extclusterapi.LocalCluster)
			if err != nil {
				return nil, err
			}

			return authactions.NewCredentialFromBearer(tokens.V3, token)
		}},
		{"v3 session token", func(client *rancher.Client) (*authactions.Credential, error) {
			_, userClient, err := rbac.SetupUser(client, rbac.StandardUser.String())
			if err != nil {
				return nil, err
			}

			return authactions.NewCredentialFromBearer(tokens.V3, userClient.Management.APIBaseClient.Opts.TokenKey)
		}},
		{"ext token", authactions.NewExtCredential},
	}

	for _, kind := range mints {
		s.Run(kind.name, func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			credential, err := kind.mint(subClient)
			require.NoError(s.T(), err, "Failed to mint a %v", kind.name)

			_, err = authactions.PlantLastUsedAt(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
			require.NoError(s.T(), err, "Failed to age the lastUsedAt of the %v", kind.name)

			requests, writes, err := authactions.RecordWrites(subClient, s.requester, credential, authactions.BearerAPIPath, defaults.OneSecondTimeout, (2 * defaults.TenSecondTimeout))
			require.NoError(s.T(), err, "Failed to record the writes of the %v", kind.name)

			assert.NoError(s.T(), authactions.VerifyAllSucceeded(requests), "Every request should be accepted")
			assert.Len(s.T(), writes, 1, "Only the first use of a %v should be written", kind.name)
		})
	}
}

func (s *TokenLastUsedAtTestSuite) TestTokenAuthenticationIsRefusedAsBefore() {
	baseline, err := authactions.ListLastUsedAtErrors(s.client)
	require.NoError(s.T(), err, "Failed to list the lastUsedAt errors Rancher had logged")

	for _, kind := range lastUsedAtKinds {
		s.Run(string(kind), func() {
			subSession := s.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := s.client.WithSession(subSession)
			require.NoError(s.T(), err, "Failed to scope the client to the test session")

			shortLived, err := authactions.NewShortLivedCredential(subClient, kind, defaults.NinetySecondTimeout)
			require.NoError(s.T(), err, "Failed to mint a %v token that lives %v", kind, defaults.NinetySecondTimeout)

			valid, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

			tampered := &authactions.Credential{Kind: valid.Kind, Name: valid.Name, Bearer: valid.Bearer + "tampered"}

			request, err := s.requester.Send(tampered, authactions.ProxyPath)
			if assert.NoError(s.T(), err, "Failed to make a request with the tampered token") {
				assert.Equal(s.T(), http.StatusUnauthorized, request.StatusCode, "A %v token with a tampered secret should be refused", kind)
			}

			disabled, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

			if assert.NoError(s.T(), authactions.SetCredentialEnabled(subClient, disabled, false), "Failed to disable the %v token", kind) {
				assert.NoError(s.T(), s.requester.WaitForStatus(disabled, authactions.ProxyPath, http.StatusUnauthorized, defaults.OneMinuteTimeout),
					"A disabled %v token should be refused", kind)
			}

			deleted, _, err := authactions.NewUsedCredential(subClient, s.requester, kind)
			require.NoError(s.T(), err, "Failed to mint and use a %v token", kind)

			if assert.NoError(s.T(), authactions.DeleteCredential(subClient, deleted), "Failed to delete the %v token", kind) {
				assert.NoError(s.T(), s.requester.WaitForStatus(deleted, authactions.ProxyPath, http.StatusUnauthorized, defaults.OneMinuteTimeout),
					"A deleted %v token should be refused", kind)
			}

			assert.NoError(s.T(), s.requester.WaitForStatus(valid, authactions.ProxyPath, http.StatusOK, defaults.OneMinuteTimeout), "The untouched %v token should still be accepted", kind)

			assert.NoError(s.T(), s.requester.WaitForStatus(shortLived, authactions.ProxyPath, http.StatusUnauthorized, 3*defaults.OneMinuteTimeout),
				"An expired %v token should be refused", kind)
		})
	}

	require.NoError(s.T(), authactions.VerifyNoLastUsedAtErrors(s.client, baseline), "Rancher should log no failed write of lastUsedAt for valid tokens")
}

func (s *TokenLastUsedAtTestSuite) TestAuditLogRecordsEveryRequestDespiteTheThrottle() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	restore, err := auditlog.EnsureRancherAuditLogging(subClient, auditlog.RancherAuditLevelHeaders)
	require.NoError(s.T(), err, "Failed to turn the Rancher audit log on")

	subSession.RegisterCleanupFunc(restore)

	for _, kind := range lastUsedAtKinds {
		s.Run(string(kind), func() {
			credential, err := authactions.NewCredential(subClient, kind)
			require.NoError(s.T(), err, "Failed to mint a %v token", kind)

			requests, err := s.requester.Repeat(credential, authactions.ProxyPath, (3 * defaults.OneSecondTimeout), (3 * defaults.TenSecondTimeout))
			require.NoError(s.T(), err, "Failed to make the requests")

			marker := authactions.RequestMarkerParameter + "=" + credential.Name

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

	credential, _, err := authactions.NewUsedCredential(subClient, s.requester, tokens.Ext)
	require.NoError(s.T(), err, "Failed to mint and use an ext token")

	planted, err := authactions.PlantLastUsedAt(subClient, credential.Target(), time.Now().Add(-defaults.TwoMinuteTimeout))
	require.NoError(s.T(), err, "Failed to age the lastUsedAt of the ext token")

	tokenBefore, err := exttokenapi.GetExtTokenByName(subClient, credential.Name)
	require.NoError(s.T(), err, "Failed to read the ext token before the write")

	secretBefore, err := secrets.GetSecretByName(subClient, extclusterapi.LocalCluster, tokens.ExtSecretNamespace, credential.Name)
	require.NoError(s.T(), err, "Failed to read the secret before the write")

	request, err := s.requester.Send(credential, authactions.ProxyPath)
	require.NoError(s.T(), err, "Failed to make a request with the ext token")
	require.Equal(s.T(), http.StatusOK, request.StatusCode, "The ext token should be accepted")

	_, err = authactions.WaitForLastUsedAtChange(subClient, credential.Target(), planted)
	require.NoError(s.T(), err, "The use more than a minute after the last should be written")

	tokenAfter, err := exttokenapi.GetExtTokenByName(subClient, credential.Name)
	require.NoError(s.T(), err, "Failed to read the ext token after the write")

	secretAfter, err := secrets.GetSecretByName(subClient, extclusterapi.LocalCluster, tokens.ExtSecretNamespace, credential.Name)
	require.NoError(s.T(), err, "Failed to read the secret after the write")

	require.NoError(s.T(), authactions.VerifyOnlyExtLastUsedAtChanged(tokenBefore, tokenAfter, secretBefore, secretAfter),
		"A write of lastUsedAt should leave every other part of the ext token alone")

	_, err = s.requester.Send(credential, authactions.ProxyPath)
	require.NoError(s.T(), err, "The ext token should still authenticate after the write")
}

func (s *TokenLastUsedAtTestSuite) TestManyTokensAreWrittenOncePerMinuteEach() {
	subSession := s.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := s.client.WithSession(subSession)
	require.NoError(s.T(), err, "Failed to scope the client to the test session")

	credentials := []*authactions.Credential{}
	names := map[tokens.Kind][]string{}

	for _, kind := range lastUsedAtKinds {
		for i := 0; i < 25; i++ {
			credential, err := authactions.NewCredential(subClient, kind)
			require.NoError(s.T(), err, "Failed to mint %v token %d", kind, i+1)

			credentials = append(credentials, credential)
			names[kind] = append(names[kind], credential.Name)
		}
	}

	watches := map[tokens.Kind]*tokens.Watch{}

	for _, kind := range lastUsedAtKinds {
		recorder, err := tokens.WatchLastUsedAt(subClient, kind, "", names[kind]...)
		require.NoError(s.T(), err, "Failed to watch the %v tokens", kind)

		watches[kind] = recorder
	}

	log.Infof("Polling %d tokens every %v for %v", len(credentials), defaults.FiveSecondTimeout, defaults.OneMinuteTimeout)

	results, requestErr := authactions.RepeatAll(s.requester, credentials, authactions.ProxyPath, defaults.FiveSecondTimeout, defaults.OneMinuteTimeout)

	<-time.After(defaults.FiveSecondTimeout)

	writes := map[string][]tokens.Write{}

	for kind, recorder := range watches {
		stopped, err := recorder.Stop()
		require.NoError(s.T(), err, "The %v watch should run for the whole poll", kind)

		for name, written := range stopped {
			writes[name] = written
		}
	}

	require.NoError(s.T(), requestErr, "Every poll should be accepted")

	for i, credential := range credentials {
		assert.NoError(s.T(), authactions.VerifyAllSucceeded(results[i]), "Every request of %v token %v should be accepted", credential.Kind, credential.Name)
		assert.NoError(s.T(), authactions.VerifyWritesPerMinute(writes[credential.Name], defaults.OneMinuteTimeout),
			"%v token %v should be written once a minute", credential.Kind, credential.Name)
	}
}

func TestTokenLastUsedAtTestSuite(t *testing.T) {
	suite.Run(t, new(TokenLastUsedAtTestSuite))
}

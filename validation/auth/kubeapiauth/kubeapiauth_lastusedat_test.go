//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11

package kubeapiauth

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/kubeapi/tokens"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var lastUsedAtKinds = []tokens.Kind{tokens.V3, tokens.Ext}

type KubeAPIAuthLastUsedAtSuite struct {
	suite.Suite
	session     *session.Session
	client      *rancher.Client
	requester   *authactions.Requester
	clusterID   string
	cluster     *management.Cluster
	restoreACE  func() error
	logBaseline map[string]string
}

func (k *KubeAPIAuthLastUsedAtSuite) SetupSuite() {
	k.session = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err, "Failed to create Rancher client")
	k.client = client
	k.requester = authactions.NewRequester(client)

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(k.T(), clusterName, "Cluster name should be set")

	k.clusterID, err = clusters.GetClusterIDByName(k.client, clusterName)
	require.NoError(k.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	k.cluster, err = k.client.Management.Cluster.ByID(k.clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", k.clusterID)

	require.Equal(k.T(), activeClusterState, k.cluster.State,
		"Cluster [%v] is %v rather than %v; an authorized cluster endpoint login needs the cluster up", clusterName, k.cluster.State, activeClusterState)

	logrus.Infof("Making sure the authorized cluster endpoint is on, which is what deploys the %v daemon set", authactions.KubeAPIAuthDaemonSet)
	k.restoreACE, err = authactions.EnsureACEEnabled(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to enable the authorized cluster endpoint on cluster [%v]", clusterName)

	k.logBaseline, err = authactions.KubeAPIAuthLogs(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what %v had logged before the suite", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TearDownSuite() {
	defer k.session.Cleanup()

	if k.restoreACE != nil {
		defer func() {
			logrus.Info("Putting the authorized cluster endpoint back the way the suite found it")
			require.NoError(k.T(), k.restoreACE(), "Failed to restore the authorized cluster endpoint")
		}()
	}

	require.NoError(k.T(), authactions.VerifyNoKubeAPIAuthErrors(k.client, k.clusterID, k.logBaseline),
		"%v should log no failed write, forbidden or conflict across the whole run", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestKubeAPIAuthRunsOnEveryControlPlaneNode() {
	image, err := authactions.KubeAPIAuthImage(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read the %v image", authactions.KubeAPIAuthDaemonSet)

	logrus.Infof("The %v daemon set runs [%v]", authactions.KubeAPIAuthDaemonSet, image)

	require.NoError(k.T(), authactions.VerifyKubeAPIAuthRunsOnEveryControlPlaneNode(k.client, k.clusterID),
		"One %v pod should be running for each control plane node", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestFirstUseThroughTheAuthorizedClusterEndpointIsWrittenDownstreamAndUpstream() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			clusterAuth := credential.ClusterAuthTarget(k.clusterID)

			unused, err := tokens.GetLastUsedAt(subClient, clusterAuth)
			require.NoError(k.T(), err, "Failed to read the cluster auth token")

			assert.Nil(k.T(), unused.Time, "A new cluster auth token should hold no lastUsedAt")

			upstreamUnused, err := tokens.GetLastUsedAt(subClient, credential.Target())
			require.NoError(k.T(), err, "Failed to read the upstream %v token", kind)

			kubeconfig, err := authactions.SyncedACEKubeconfig(k.client, k.client, k.clusterID)
			require.NoError(k.T(), err, "Failed to generate an authorized cluster endpoint kubeconfig")

			clientsets, err := authactions.ACEClientsetsWithToken(kubeconfig, credential.Bearer)
			require.NoError(k.T(), err, "Failed to build a client that authenticates with the %v token", kind)

			contexts := slices.Sorted(maps.Keys(clientsets))
			require.NotEmpty(k.T(), contexts, "The kubeconfig should name an authorized cluster endpoint context")

			requestedAt := time.Now()

			_, err = clientsets[contexts[0]].CoreV1().Namespaces().Get(context.Background(), "default", metav1.GetOptions{})
			require.NoError(k.T(), err, "The %v token should reach the cluster through context [%v]", kind, contexts[0])

			written, err := authactions.WaitForLastUsedAtChange(subClient, clusterAuth, unused)
			require.NoError(k.T(), err, "The first use of the token should be written to the cluster auth token")

			assert.NoError(k.T(), authactions.VerifyLastUsedAtNear(written, requestedAt), "The cluster auth token should hold the time of the request")

			copied, err := authactions.WaitForLastUsedAtChange(subClient, credential.Target(), upstreamUnused)
			require.NoError(k.T(), err, "Rancher should copy the first use to the upstream %v token", kind)

			assert.Equal(k.T(), written.Time.Unix(), copied.Time.Unix(), "The upstream token should hold the value of the cluster auth token")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestSteadyReviewsWriteTheClusterAuthTokenOnceAMinuteAndTheUpstreamTokenFollows() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			downstream, err := tokens.WatchLastUsedAt(subClient, tokens.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to watch the cluster auth token")

			upstream, err := tokens.WatchLastUsedAt(subClient, kind, "", credential.Name)
			require.NoError(k.T(), err, "Failed to watch the upstream %v token", kind)

			logrus.Infof("Reviewing the %v token every %v for %v", kind, (defaults.FiveHundredMillisecondTimeout / 5), defaults.TwoMinuteTimeout)

			requests, reviewErr := authactions.ReviewOnCadence(subClient, k.clusterID, credential.Bearer, (defaults.FiveHundredMillisecondTimeout / 5), defaults.TwoMinuteTimeout)

			<-time.After((3 * defaults.FiveSecondTimeout))

			downstreamWrites, downstreamErr := downstream.Stop()
			upstreamWrites, upstreamErr := upstream.Stop()

			require.NoError(k.T(), reviewErr, "Failed to send the reviews")

			assert.NoError(k.T(), downstreamErr, "The cluster auth token watch should run for the whole loop")
			assert.NoError(k.T(), upstreamErr, "The upstream token watch should run for the whole loop")
			assert.Greater(k.T(), len(requests), 300, "The loop should be fast enough to write once a second without the throttle")
			assert.NoError(k.T(), authactions.VerifyAllReviewed(requests), "Every review should authenticate the token")

			span := requests[len(requests)-1].At.Sub(requests[0].At)

			assert.NoError(k.T(), authactions.VerifyWritesPerMinute(downstreamWrites[credential.Name], span), "The cluster auth token should be written once a minute")
			assert.NoError(k.T(), authactions.VerifyWritesMatchRequests(downstreamWrites[credential.Name], requests), "Each lastUsedAt should be the exact second of a review")
			assert.NoError(k.T(), authactions.VerifyWritesPerMinute(upstreamWrites[credential.Name], span), "The upstream %v token should change about once a minute too", kind)
			assert.NoError(k.T(), authactions.VerifyWritesAreCopies(upstreamWrites[credential.Name], downstreamWrites[credential.Name]),
				"The upstream %v token should only ever hold values the cluster auth token held", kind)
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestInfrequentReviewsAreWrittenOnEveryReview() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			downstream, err := tokens.WatchLastUsedAt(subClient, tokens.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to watch the cluster auth token")

			requests := []authactions.Request{}

			var reviewErr error

			for review := 0; review < 3 && reviewErr == nil; review++ {
				if review > 0 {
					<-time.After(defaults.NinetySecondTimeout)
				}

				var run *authactions.ReviewRun

				run, reviewErr = authactions.SendTokenReviews(subClient, k.clusterID, time.Time{}, authactions.ReviewPlan{Token: credential.Bearer, Count: 1})
				if reviewErr == nil {
					requests = append(requests, run.Requests[0]...)
				}
			}

			<-time.After(defaults.FiveSecondTimeout)

			writes, watchErr := downstream.Stop()

			require.NoError(k.T(), reviewErr, "Failed to send the reviews")

			assert.NoError(k.T(), watchErr, "The cluster auth token watch should run for the whole loop")
			assert.NoError(k.T(), authactions.VerifyAllReviewed(requests), "Every review should authenticate the token")
			assert.NoError(k.T(), authactions.VerifyEveryRequestWasWritten(writes[credential.Name], requests), "Each review more than a minute after the last should be written")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestConcurrentReviewsLogNoConflictAndWriteOnce() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			startAt := time.Now().Add(4 * defaults.TenSecondTimeout)

			_, err = authactions.PlantLastUsedAt(subClient, credential.ClusterAuthTarget(k.clusterID), startAt.Add(-defaults.TwoMinuteTimeout))
			require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			downstream, err := tokens.WatchLastUsedAt(subClient, tokens.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to watch the cluster auth token")

			plan := authactions.ReviewPlan{Token: credential.Bearer, Count: 20, Parallel: true, Bursts: 3, BurstPause: defaults.FiveSecondTimeout}

			run, reviewErr := authactions.SendTokenReviews(subClient, k.clusterID, startAt, plan)

			<-time.After(defaults.FiveSecondTimeout)

			writes, watchErr := downstream.Stop()

			require.NoError(k.T(), reviewErr, "Failed to send the bursts")

			assert.NoError(k.T(), watchErr, "The cluster auth token watch should run for the whole job")
			assert.Len(k.T(), run.Requests[0], 20*3, "Every review of every burst should be answered")
			assert.NoError(k.T(), authactions.VerifyAllReviewed(run.Requests[0]), "Every review should authenticate the token")
			assert.NoError(k.T(), authactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "Concurrent reviews should log no conflict")

			assert.GreaterOrEqual(k.T(), len(writes[credential.Name]), 1, "The first burst after the minute expired should be written")
			assert.LessOrEqual(k.T(), len(writes[credential.Name]), 2, "Three bursts inside one minute should write once, or twice across a second boundary")
			assert.NoError(k.T(), authactions.VerifyWritesMatchRequests(writes[credential.Name], run.Requests[0][:20]), "Only the first burst should write")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestConcurrentReviewsOnEveryControlPlaneNodeWriteOnce() {
	nodes, err := authactions.ControlPlaneNodeCount(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to count the control plane nodes")

	if nodes < 2 {
		k.T().Skip(fmt.Sprintf("The cluster has %d control plane node, and reviews can only reach several %v pods on 2 or more", nodes, authactions.KubeAPIAuthDaemonSet))
	}

	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			downstream, err := tokens.WatchLastUsedAt(subClient, tokens.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to watch the cluster auth token")

			jobs := nodes * 2
			runs := make([]*authactions.ReviewRun, jobs)
			failures := make([]error, jobs)
			plan := authactions.ReviewPlan{Token: credential.Bearer, Count: int(defaults.OneMinuteTimeout / defaults.OneSecondTimeout), Interval: defaults.OneSecondTimeout}
			finished := make(chan int, jobs)

			for job := 0; job < jobs; job++ {
				go func() {
					runs[job], failures[job] = authactions.SendTokenReviews(subClient, k.clusterID, time.Time{}, plan)
					finished <- job
				}()
			}

			for job := 0; job < jobs; job++ {
				<-finished
			}

			<-time.After(defaults.FiveSecondTimeout)

			writes, watchErr := downstream.Stop()

			reached := map[string]struct{}{}
			requests := []authactions.Request{}

			for job := range runs {
				require.NoError(k.T(), failures[job], "Failed to send the reviews of job %d", job+1)

				reached[runs[job].Node] = struct{}{}
				requests = append(requests, runs[job].Requests[0]...)
			}

			assert.NoError(k.T(), watchErr, "The cluster auth token watch should run for the whole job")
			assert.NoError(k.T(), authactions.VerifyAllReviewed(requests), "Every review should authenticate the token")
			assert.NoError(k.T(), authactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "No %v pod should log a conflict", authactions.KubeAPIAuthDaemonSet)
			assert.NoError(k.T(), authactions.VerifyWritesPerMinute(writes[credential.Name], defaults.OneMinuteTimeout), "Several pods should write the token once a minute between them")

			if len(reached) < 2 {
				k.T().Skip(fmt.Sprintf("All %d jobs ran on one control plane node, so reviews never reached several %v pods", jobs, authactions.KubeAPIAuthDaemonSet))
			}
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestStoredClusterAuthTokenLastUsedAtIsWrittenOnlyBeyondAMinute() {
	farFuture := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	epoch := time.Unix(0, 0).UTC()

	stored := []struct {
		name      string
		offset    time.Duration
		absolute  *time.Time
		overwrite bool
	}{
		{"30s ahead", 30 * defaults.OneSecondTimeout, nil, false},
		{"30s behind", -30 * defaults.OneSecondTimeout, nil, false},
		{"50s ahead", 50 * defaults.OneSecondTimeout, nil, false},
		{"50s behind", -50 * defaults.OneSecondTimeout, nil, false},
		{"90s ahead", 90 * defaults.OneSecondTimeout, nil, true},
		{"90s behind", -90 * defaults.OneSecondTimeout, nil, true},
		{"70s ahead", 70 * defaults.OneSecondTimeout, nil, true},
		{"70s behind", -70 * defaults.OneSecondTimeout, nil, true},
		{"year 2099", 0, &farFuture, true},
		{"the epoch", 0, &epoch, true},
	}

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

	type scenario struct {
		credential *authactions.Credential
		planted    *tokens.LastUsedAt
	}

	scenarios := map[string]scenario{}
	plans := []authactions.ReviewPlan{}
	keys := []string{}
	credentials := []*authactions.Credential{}

	for _, kind := range lastUsedAtKinds {
		for _, value := range stored {
			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			credentials = append(credentials, credential)
			keys = append(keys, fmt.Sprintf("%v/%v", kind, value.name))
			plans = append(plans, authactions.ReviewPlan{Token: credential.Bearer, Count: 1})
		}
	}

	startAt := time.Now().Add(4 * defaults.TenSecondTimeout)

	type sentReviews struct {
		run *authactions.ReviewRun
		err error
	}

	sent := make(chan sentReviews, 1)

	go func() {
		run, sendErr := authactions.SendTokenReviews(subClient, k.clusterID, startAt, plans...)
		sent <- sentReviews{run, sendErr}
	}()

	targets := make([]tokens.Target, len(credentials))
	plantTimes := make([]time.Time, len(credentials))

	for index, credential := range credentials {
		value := stored[index%len(stored)]

		targets[index] = credential.ClusterAuthTarget(k.clusterID)
		plantTimes[index] = startAt.Add(value.offset)

		if value.absolute != nil {
			plantTimes[index] = *value.absolute
		}
	}

	planted, err := authactions.PlantLastUsedAtConcurrently(subClient, targets, plantTimes)
	require.NoError(k.T(), err, "Failed to plant the stored lastUsedAt values")

	for index, credential := range credentials {
		scenarios[keys[index]] = scenario{credential: credential, planted: planted[index]}
	}

	require.True(k.T(), time.Now().Before(startAt), "Planting the stored values should finish before the reviews start")

	logrus.Infof("Reviewing %d tokens at once", len(plans))

	result := <-sent
	require.NoError(k.T(), result.err, "Failed to send the reviews")

	run := result.run

	for index, key := range keys {
		overwrite := stored[index%len(stored)].overwrite
		current := scenarios[key]

		k.Run(key, func() {
			require.Len(k.T(), run.Requests[index], 1, "The review should be answered")

			assert.NoError(k.T(), authactions.VerifyAllReviewed(run.Requests[index]), "The review should authenticate the token")

			target := current.credential.ClusterAuthTarget(k.clusterID)

			if !overwrite {
				assert.NoError(k.T(), authactions.VerifyLastUsedAtUnchanged(subClient, target, current.planted), "A stored value within a minute of the review should be left alone")

				return
			}

			after, err := tokens.GetLastUsedAt(subClient, target)
			require.NoError(k.T(), err, "Failed to read the cluster auth token")

			assert.NoError(k.T(), authactions.VerifyLastUsedAtNear(after, run.Requests[index][0].At), "A stored value a minute or more from the review should be overwritten with its time")
		})
	}

	require.NoError(k.T(), authactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "%v should log no error for any stored value", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestLastUsedAtWriteChangesNothingElseOnTheClusterAuthToken() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			clusterAuth := credential.ClusterAuthTarget(k.clusterID)

			planted, err := authactions.PlantLastUsedAt(subClient, clusterAuth, time.Now().Add(-defaults.TwoMinuteTimeout))
			require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

			before, err := tokens.GetClusterAuthToken(subClient, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to read the cluster auth token before the review")

			_, err = authactions.SendTokenReviews(subClient, k.clusterID, time.Time{}, authactions.ReviewPlan{Token: credential.Bearer, Count: 1})
			require.NoError(k.T(), err, "Failed to send the review")

			_, err = authactions.WaitForLastUsedAtChange(subClient, clusterAuth, planted)
			require.NoError(k.T(), err, "The review should be written")

			after, err := tokens.GetClusterAuthToken(subClient, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to read the cluster auth token after the review")

			assert.NoError(k.T(), authactions.VerifyOnlyLastUsedAtChanged(before, after), "The write should leave every other field of the cluster auth token alone")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestLastUsedAtWriteDoesNotRevertADisabledToken() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			requests, disabledAt, err := authactions.ReviewAcrossDisable(subClient, k.clusterID, credential, 60, defaults.OneSecondTimeout, (3 * defaults.FiveSecondTimeout))
			require.NoError(k.T(), err, "Failed to review the token across its being disabled")

			after := []authactions.Request{}

			for _, request := range requests {
				if request.At.After(disabledAt.Add((3 * defaults.FiveSecondTimeout))) {
					after = append(after, request)
				}
			}

			assert.GreaterOrEqual(k.T(), len(after), 5, "Enough reviews should come after the disable for it to be tested")

			for _, request := range after {
				assert.False(k.T(), request.Authenticated, "A review at %v, after the token was disabled, should be refused", request.At.UTC().Format(time.RFC3339))
			}

			clusterAuthToken, err := tokens.GetClusterAuthToken(subClient, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to read the cluster auth token")

			assert.False(k.T(), clusterAuthToken.Enabled, "Writes of lastUsedAt should not switch the cluster auth token back on")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestKubeAPIAuthMayPatchClusterAuthTokens() {
	require.NoError(k.T(), authactions.VerifyKubeAPIAuthCanPatchClusterAuthTokens(k.client, k.clusterID),
		"The %v service account should be allowed to patch cluster auth tokens", authactions.KubeAPIAuthDaemonSet)

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, tokens.V3)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	clusterAuth := credential.ClusterAuthTarget(k.clusterID)

	planted, err := authactions.PlantLastUsedAt(subClient, clusterAuth, time.Now().Add(-defaults.TwoMinuteTimeout))
	require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

	baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

	_, err = authactions.SendTokenReviews(subClient, k.clusterID, time.Time{}, authactions.ReviewPlan{Token: credential.Bearer, Count: 1})
	require.NoError(k.T(), err, "Failed to send the review")

	_, err = authactions.WaitForLastUsedAtChange(subClient, clusterAuth, planted)
	require.NoError(k.T(), err, "The patch of lastUsedAt should go through")

	require.NoError(k.T(), authactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "%v should log no forbidden", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestLastUsedAtResumesAfterTheDaemonSetRestarts() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, tokens.V3)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	logrus.Infof("Restarting the %v daemon set", authactions.KubeAPIAuthDaemonSet)
	require.NoError(k.T(), authactions.RestartKubeAPIAuth(subClient, k.clusterID), "Failed to restart the %v daemon set", authactions.KubeAPIAuthDaemonSet)

	clusterAuth := credential.ClusterAuthTarget(k.clusterID)

	planted, err := authactions.PlantLastUsedAt(subClient, clusterAuth, time.Now().Add(-defaults.TwoMinuteTimeout))
	require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

	run, err := authactions.SendTokenReviews(subClient, k.clusterID, time.Time{}, authactions.ReviewPlan{Token: credential.Bearer, Count: 1})
	require.NoError(k.T(), err, "Failed to send the review")
	require.NoError(k.T(), authactions.VerifyAllReviewed(run.Requests[0]), "The restarted %v should authenticate the token", authactions.KubeAPIAuthDaemonSet)

	changed, err := authactions.WaitForLastUsedAtChange(subClient, clusterAuth, planted)
	require.NoError(k.T(), err, "The restarted %v should write lastUsedAt again", authactions.KubeAPIAuthDaemonSet)
	require.NoError(k.T(), authactions.VerifyLastUsedAtNear(changed, run.Requests[0][0].At), "lastUsedAt should hold the time of the review")
}

func (k *KubeAPIAuthLastUsedAtSuite) TestAlternatingProxyAndClusterEndpointUseNeverMovesTheUpstreamTokenBack() {
	for _, kind := range lastUsedAtKinds {
		k.Run(string(kind), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := authactions.NewClusterAuthCredential(subClient, k.clusterID, kind)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", kind)

			upstream, err := tokens.WatchLastUsedAt(subClient, kind, "", credential.Name)
			require.NoError(k.T(), err, "Failed to watch the upstream %v token", kind)

			reviews, proxied, useErr := authactions.ReviewAlongsideRequests(subClient, k.requester, k.clusterID, credential, defaults.TenSecondTimeout, defaults.OneMinuteTimeout)

			<-time.After((3 * defaults.FiveSecondTimeout))

			writes, watchErr := upstream.Stop()

			require.NoError(k.T(), useErr, "Failed to use the token through both paths")

			assert.NoError(k.T(), watchErr, "The upstream token watch should run for the whole loop")
			assert.NoError(k.T(), authactions.VerifyAllReviewed(reviews), "Every review should authenticate the token")
			assert.NoError(k.T(), authactions.VerifyAllSucceeded(proxied), "Every request through the Rancher proxy should be accepted")
			assert.NoError(k.T(), authactions.VerifyWritesNeverMoveBack(writes[credential.Name]), "Two writers should not move the upstream lastUsedAt back")
			assert.LessOrEqual(k.T(), len(writes[credential.Name]), 4, "Rancher and kube-api-auth should each write about once a minute")
		})
	}
}

func TestKubeAPIAuthLastUsedAtSuite(t *testing.T) {
	suite.Run(t, new(KubeAPIAuthLastUsedAtSuite))
}

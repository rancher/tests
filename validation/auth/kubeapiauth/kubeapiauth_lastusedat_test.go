//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package kubeapiauth

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	kubeapiauthactions "github.com/rancher/tests/actions/auth/kubeapiauth"
	"github.com/rancher/tests/actions/kubeapi/workloads/daemonsets"
	tokenapi "github.com/rancher/tests/actions/tokens"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var lastUsedAtCategories = []tokenapi.Category{tokenapi.V3, tokenapi.Ext}

type KubeAPIAuthLastUsedAtSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	requester  *tokenapi.Requester
	clusterID  string
	reviewPods []*kubeapiauthactions.TokenReviewPod
}

func (k *KubeAPIAuthLastUsedAtSuite) SetupSuite() {
	k.session = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err, "Failed to create Rancher client")
	k.client = client
	k.requester = tokenapi.NewRequester(client)

	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(k.T(), clusterName, "Cluster name should be set")

	k.clusterID, err = clusters.GetClusterIDByName(k.client, clusterName)
	require.NoError(k.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	cluster, err := k.client.Management.Cluster.ByID(k.clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", k.clusterID)

	require.Equal(k.T(), activeClusterState, cluster.State,
		"Cluster [%v] is %v rather than %v; an authorized cluster endpoint login needs the cluster up", clusterName, cluster.State, activeClusterState)

	k.T().Logf("Making sure the authorized cluster endpoint is on, which is what deploys the %v daemon set", authactions.KubeAPIAuthDaemonSet)
	restoreACE, err := authactions.EnsureACEEnabled(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to enable the authorized cluster endpoint on cluster [%v]", clusterName)
	k.session.RegisterCleanupFunc(restoreACE)

	k.reviewPods, err = kubeapiauthactions.CreateTokenReviewPods(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to start a TokenReview pod on every control plane node")
}

func (k *KubeAPIAuthLastUsedAtSuite) TearDownSuite() {
	k.session.Cleanup()
}

func (k *KubeAPIAuthLastUsedAtSuite) TestKubeAPIAuthRunsOnEveryControlPlaneNode() {
	image, err := authactions.KubeAPIAuthImage(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read the %v image", authactions.KubeAPIAuthDaemonSet)

	k.T().Logf("The %v daemon set runs [%v]", authactions.KubeAPIAuthDaemonSet, image)

	require.NoError(k.T(), kubeapiauthactions.VerifyKubeAPIAuthRunsOnEveryControlPlaneNode(k.client, k.clusterID),
		"One %v pod should be running for each control plane node", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestFirstUseThroughTheAuthorizedClusterEndpointIsWrittenDownstreamAndUpstream() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			clusterAuth := credential.ClusterAuthTarget(k.clusterID)

			unused, err := tokenapi.GetLastUsedAt(subClient, clusterAuth)
			require.NoError(k.T(), err, "Failed to read the cluster auth token")

			assert.Nil(k.T(), unused, "A new cluster auth token should hold no lastUsedAt")

			upstreamUnused, err := tokenapi.GetLastUsedAt(subClient, credential.Target())
			require.NoError(k.T(), err, "Failed to read the upstream %v token", category)

			kubeconfig, err := authactions.SyncedACEKubeconfig(subClient, subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to generate an authorized cluster endpoint kubeconfig")

			clientsets, err := authactions.ACEClientsetsWithToken(kubeconfig, credential.Bearer)
			require.NoError(k.T(), err, "Failed to build a client that authenticates with the %v token", category)

			contexts := slices.Sorted(maps.Keys(clientsets))
			require.NotEmpty(k.T(), contexts, "The kubeconfig should name an authorized cluster endpoint context")

			requestedAt := time.Now()

			_, err = clientsets[contexts[0]].CoreV1().Namespaces().Get(context.Background(), "default", metav1.GetOptions{})
			require.NoError(k.T(), err, "The %v token should reach the cluster through context [%v]", category, contexts[0])

			written, err := tokenapi.WaitForLastUsedAtChange(subClient, clusterAuth, unused)
			require.NoError(k.T(), err, "The first use of the token should be written to the cluster auth token")

			assert.NoError(k.T(), tokenapi.VerifyLastUsedAtNear(written, requestedAt), "The cluster auth token should hold the time of the request")

			copied, err := tokenapi.WaitForLastUsedAtChange(subClient, credential.Target(), upstreamUnused)
			require.NoError(k.T(), err, "Rancher should copy the first use to the upstream %v token", category)

			assert.Equal(k.T(), written.Unix(), copied.Unix(), "The upstream token should hold the value of the cluster auth token")

			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
				"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestSteadyReviewsWriteTheClusterAuthTokenOnceAMinuteAndTheUpstreamTokenFollows() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			downstream, err := tokenapi.RecordLastUsedAt(subClient, tokenapi.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to start recording the lastUsedAt of the cluster auth token")

			upstream, err := tokenapi.RecordLastUsedAt(subClient, category, "", credential.Name)
			require.NoError(k.T(), err, "Failed to start recording the lastUsedAt of the upstream %v token", category)

			k.T().Logf("Reviewing the %v token every %v for %v", category, defaults.FiveHundredMillisecondTimeout/5, defaults.TwoMinuteTimeout)

			requests, reviewErr := kubeapiauthactions.SendTokenReviewsOnCadence(k.reviewPods[0], credential.Bearer, defaults.FiveHundredMillisecondTimeout/5, defaults.TwoMinuteTimeout)

			downstreamWrites, downstreamErr := downstream.StopWhenQuiet(3 * defaults.FiveSecondTimeout)
			upstreamWrites, upstreamErr := upstream.StopWhenQuiet(3 * defaults.FiveSecondTimeout)

			require.NoError(k.T(), reviewErr, "Failed to send the reviews")
			require.GreaterOrEqual(k.T(), len(requests), int(defaults.TwoMinuteTimeout/defaults.OneSecondTimeout), "The reviews should come at least once a second, faster than the throttle allows a write")

			assert.NoError(k.T(), downstreamErr, "The cluster auth token lastUsedAt should be read for the whole loop")
			assert.NoError(k.T(), upstreamErr, "The upstream token lastUsedAt should be read for the whole loop")
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed(requests), "Every review should authenticate the token")

			span := requests[len(requests)-1].At.Sub(requests[0].At)

			assert.NoError(k.T(), tokenapi.VerifyWritesPerMinute(downstreamWrites[credential.Name], span), "The cluster auth token should be written once a minute")
			assert.NoError(k.T(), tokenapi.VerifyWritesMatchRequests(downstreamWrites[credential.Name], requests), "Each lastUsedAt should be the exact second of a review")
			assert.NoError(k.T(), tokenapi.VerifyWritesPerMinute(upstreamWrites[credential.Name], span), "The upstream %v token should change about once a minute too", category)
			assert.NoError(k.T(), kubeapiauthactions.VerifyWritesAreCopies(upstreamWrites[credential.Name], downstreamWrites[credential.Name]),
				"The upstream %v token should only ever hold values the cluster auth token held", category)

			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
				"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestInfrequentReviewsAreWrittenOnEveryReview() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			downstream, err := tokenapi.RecordLastUsedAt(subClient, tokenapi.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to start recording the lastUsedAt of the cluster auth token")

			requests, reviewErr := kubeapiauthactions.SendTokenReviewsOnCadence(k.reviewPods[0], credential.Bearer, defaults.NinetySecondTimeout, 3*defaults.NinetySecondTimeout)

			writes, recordErr := downstream.StopWhenQuiet(defaults.FiveSecondTimeout)

			require.NoError(k.T(), reviewErr, "Failed to send the reviews")

			assert.NoError(k.T(), recordErr, "The cluster auth token lastUsedAt should be read for the whole loop")
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed(requests), "Every review should authenticate the token")
			assert.NoError(k.T(), tokenapi.VerifyEveryRequestWasWritten(writes[credential.Name], requests), "Each review more than a minute after the last should be written")

			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
				"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestConcurrentReviewsLogNoConflictAndWriteOnce() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			_, err = tokenapi.SetLastUsedAtAndWait(subClient, credential.ClusterAuthTarget(k.clusterID), time.Now().Add(-defaults.TwoMinuteTimeout))
			require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			downstream, err := tokenapi.RecordLastUsedAt(subClient, tokenapi.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to start recording the lastUsedAt of the cluster auth token")

			bursts, reviewErr := kubeapiauthactions.SendTokenReviewBursts(k.reviewPods[0], credential.Bearer, 3, 20, 2*defaults.FiveSecondTimeout)

			writes, recordErr := downstream.StopWhenQuiet(defaults.FiveSecondTimeout)

			require.NoError(k.T(), reviewErr, "Failed to send the bursts")
			require.Len(k.T(), slices.Concat(bursts...), 20*3, "Every review of every burst should be answered")

			assert.NoError(k.T(), recordErr, "The cluster auth token lastUsedAt should be read for the whole job")
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed(slices.Concat(bursts...)), "Every review should authenticate the token")
			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "Concurrent reviews should log no conflict")

			assert.GreaterOrEqual(k.T(), len(writes[credential.Name]), 1, "The first burst after the minute expired should be written")
			assert.LessOrEqual(k.T(), len(writes[credential.Name]), 2, "Three bursts inside one minute should write once, or twice across a second boundary")
			assert.NoError(k.T(), tokenapi.VerifyWritesMatchRequests(writes[credential.Name], bursts[0]), "Only the first burst should write")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestConcurrentReviewsOnEveryControlPlaneNodeWriteOnce() {
	nodes, err := authactions.ControlPlaneNodeCount(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to count the control plane nodes")

	if nodes < 2 {
		k.T().Skip(fmt.Sprintf("The cluster has %d control plane node, and reviews can only reach several %v pods on 2 or more", nodes, authactions.KubeAPIAuthDaemonSet))
	}

	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			downstream, err := tokenapi.RecordLastUsedAt(subClient, tokenapi.ClusterAuth, k.clusterID, credential.Name)
			require.NoError(k.T(), err, "Failed to start recording the lastUsedAt of the cluster auth token")

			requests, reviewErr := kubeapiauthactions.SendTokenReviewsOnCadenceFromEveryPod(k.reviewPods, credential.Bearer, defaults.OneSecondTimeout, defaults.OneMinuteTimeout)

			writes, recordErr := downstream.StopWhenQuiet(defaults.FiveSecondTimeout)

			require.NoError(k.T(), reviewErr, "Failed to send the reviews")

			assert.NoError(k.T(), recordErr, "The cluster auth token lastUsedAt should be read for the whole job")
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed(requests), "Every review should authenticate the token")
			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "No %v pod should log a conflict", authactions.KubeAPIAuthDaemonSet)
			assert.NoError(k.T(), tokenapi.VerifyWritesPerMinute(writes[credential.Name], defaults.OneMinuteTimeout), "Several pods should write the token once a minute between them")
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestStoredClusterAuthTokenLastUsedAtIsWrittenOnlyBeyondAMinute() {
	farFuture := time.Date(2099, time.January, 1, 0, 0, 0, 0, time.UTC)
	epoch := time.Unix(0, 0).UTC()

	type storedValue struct {
		name     string
		offset   time.Duration
		absolute *time.Time
	}

	withinAMinute := []storedValue{
		{"30s ahead", 30 * defaults.OneSecondTimeout, nil},
		{"30s behind", -30 * defaults.OneSecondTimeout, nil},
		{"50s ahead", 50 * defaults.OneSecondTimeout, nil},
		{"50s behind", -50 * defaults.OneSecondTimeout, nil},
	}

	beyondAMinute := []storedValue{
		{"90s ahead", 90 * defaults.OneSecondTimeout, nil},
		{"90s behind", -90 * defaults.OneSecondTimeout, nil},
		{"70s ahead", 70 * defaults.OneSecondTimeout, nil},
		{"70s behind", -70 * defaults.OneSecondTimeout, nil},
		{"year 2099", 0, &farFuture},
		{"the epoch", 0, &epoch},
	}

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

	stored := append(slices.Clone(withinAMinute), beyondAMinute...)
	keys := []string{}
	credentials := []*tokenapi.Credential{}

	for _, category := range lastUsedAtCategories {
		for _, value := range stored {
			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			keys = append(keys, fmt.Sprintf("%v/%v", category, value.name))
			credentials = append(credentials, credential)
		}
	}

	startAt := time.Now().Add(4 * defaults.TenSecondTimeout)
	plantTimes := []time.Time{}

	for index := range credentials {
		value := stored[index%len(stored)]

		plantedAt := startAt.Add(value.offset)
		if value.absolute != nil {
			plantedAt = *value.absolute
		}

		plantTimes = append(plantTimes, plantedAt)
	}

	k.T().Logf("Reviewing %d tokens at once", len(credentials))

	clusterAuthTargets := []tokenapi.Target{}
	bearers := []string{}

	for _, credential := range credentials {
		clusterAuthTargets = append(clusterAuthTargets, credential.ClusterAuthTarget(k.clusterID))
		bearers = append(bearers, credential.Bearer)
	}

	planted, err := tokenapi.SetLastUsedAtAndWaitUntil(subClient, clusterAuthTargets, plantTimes, startAt)
	require.NoError(k.T(), err, "Failed to plant the stored values")

	requests, err := kubeapiauthactions.SendTokenReviewsConcurrently(k.reviewPods[0], bearers)
	require.NoError(k.T(), err, "Failed to review the tokens")

	unchanged := tokenapi.VerifyLastUsedAtUnchanged(subClient, clusterAuthTargets, planted)

	for index, key := range keys {
		if index%len(stored) >= len(withinAMinute) {
			continue
		}

		k.Run(key, func() {
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed([]tokenapi.Request{requests[index]}), "The review should authenticate the token")
			assert.NoError(k.T(), unchanged[index], "A stored value within a minute of the review should be left alone")
		})
	}

	for index, key := range keys {
		if index%len(stored) < len(withinAMinute) {
			continue
		}

		k.Run(key, func() {
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed([]tokenapi.Request{requests[index]}), "The review should authenticate the token")

			after, err := tokenapi.WaitForLastUsedAtChange(subClient, credentials[index].ClusterAuthTarget(k.clusterID), planted[index])
			require.NoError(k.T(), err, "The review should overwrite a stored value a minute or more away")

			assert.NoError(k.T(), tokenapi.VerifyLastUsedAtNear(after, requests[index].At), "A stored value a minute or more from the review should be overwritten with its time")
		})
	}

	require.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "%v should log no error for any stored value", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestLastUsedAtWriteChangesNothingElseOnTheClusterAuthToken() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			clusterAuth := credential.ClusterAuthTarget(k.clusterID)

			planted, err := tokenapi.SetLastUsedAtAndWait(subClient, clusterAuth, time.Now().Add(-defaults.TwoMinuteTimeout))
			require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

			downstream, err := subClient.WranglerContext.DownStreamClusterWranglerContext(k.clusterID)
			require.NoError(k.T(), err, "Failed to reach the downstream cluster")

			before, err := downstream.Cluster.ClusterAuthToken().Get(authactions.KubeAPIAuthNamespace, credential.Name, metav1.GetOptions{})
			require.NoError(k.T(), err, "Failed to read the cluster auth token before the review")

			_, err = k.reviewPods[0].SendTokenReview(credential.Bearer)
			require.NoError(k.T(), err, "Failed to send the review")

			_, err = tokenapi.WaitForLastUsedAtChange(subClient, clusterAuth, planted)
			require.NoError(k.T(), err, "The review should be written")

			after, err := downstream.Cluster.ClusterAuthToken().Get(authactions.KubeAPIAuthNamespace, credential.Name, metav1.GetOptions{})
			require.NoError(k.T(), err, "Failed to read the cluster auth token after the review")

			assert.NoError(k.T(), kubeapiauthactions.VerifyOnlyLastUsedAtChanged(before, after), "The write should leave every other field of the cluster auth token alone")

			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
				"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestLastUsedAtWriteDoesNotRevertADisabledToken() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			requests, disableRequestedAt, disabledAt, err := kubeapiauthactions.DisableTokenDuringTokenReviews(subClient, k.reviewPods[0], k.clusterID, credential, defaults.OneSecondTimeout, defaults.NinetySecondTimeout)
			require.NoError(k.T(), err, "Failed to review the token across its being disabled")

			written, err := tokenapi.GetLastUsedAt(subClient, credential.ClusterAuthTarget(k.clusterID))
			require.NoError(k.T(), err, "Failed to read the cluster auth token lastUsedAt")
			require.NotNil(k.T(), written, "The cluster auth token should carry a lastUsedAt")
			require.WithinDuration(k.T(), disableRequestedAt, *written, 2*defaults.FiveSecondTimeout, "A lastUsedAt write should land right before the disable request")

			after := []tokenapi.Request{}

			for _, request := range requests {
				if request.At.After(disabledAt.Add(3 * defaults.FiveSecondTimeout)) {
					after = append(after, request)
				}
			}

			require.GreaterOrEqual(k.T(), len(after), 5, "Enough reviews should come after the disable for it to be tested")

			for _, request := range after {
				assert.False(k.T(), request.Authenticated, "A review at %v, after the token was disabled, should be refused", request.At.UTC().Format(time.RFC3339))
			}

			downstream, err := subClient.WranglerContext.DownStreamClusterWranglerContext(k.clusterID)
			require.NoError(k.T(), err, "Failed to reach the downstream cluster")

			clusterAuthToken, err := downstream.Cluster.ClusterAuthToken().Get(authactions.KubeAPIAuthNamespace, credential.Name, metav1.GetOptions{})
			require.NoError(k.T(), err, "Failed to read the cluster auth token")

			assert.False(k.T(), clusterAuthToken.Enabled, "Writes of lastUsedAt should not switch the cluster auth token back on")

			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
				"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
		})
	}
}

func (k *KubeAPIAuthLastUsedAtSuite) TestKubeAPIAuthMayPatchClusterAuthTokens() {
	require.NoError(k.T(), kubeapiauthactions.VerifyKubeAPIAuthCanPatchClusterAuthTokens(k.client, k.clusterID),
		"The %v service account should be allowed to patch cluster auth tokens", authactions.KubeAPIAuthDaemonSet)

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, tokenapi.V3)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	clusterAuth := credential.ClusterAuthTarget(k.clusterID)

	planted, err := tokenapi.SetLastUsedAtAndWait(subClient, clusterAuth, time.Now().Add(-defaults.TwoMinuteTimeout))
	require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

	baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

	_, err = k.reviewPods[0].SendTokenReview(credential.Bearer)
	require.NoError(k.T(), err, "Failed to send the review")

	_, err = tokenapi.WaitForLastUsedAtChange(subClient, clusterAuth, planted)
	require.NoError(k.T(), err, "The patch of lastUsedAt should go through")

	require.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline), "%v should log no forbidden", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestLastUsedAtResumesAfterTheDaemonSetRestarts() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, tokenapi.V3)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	k.T().Logf("Restarting the %v daemon set", authactions.KubeAPIAuthDaemonSet)
	require.NoError(k.T(), daemonsets.RestartDaemonSet(subClient, k.clusterID, authactions.KubeAPIAuthNamespace, authactions.KubeAPIAuthDaemonSet),
		"Failed to restart the %v daemon set", authactions.KubeAPIAuthDaemonSet)

	baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

	clusterAuth := credential.ClusterAuthTarget(k.clusterID)

	planted, err := tokenapi.SetLastUsedAtAndWait(subClient, clusterAuth, time.Now().Add(-defaults.TwoMinuteTimeout))
	require.NoError(k.T(), err, "Failed to age the lastUsedAt of the cluster auth token")

	request, err := k.reviewPods[0].SendTokenReview(credential.Bearer)
	require.NoError(k.T(), err, "Failed to send the review")
	require.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed([]tokenapi.Request{request}), "The restarted %v should authenticate the token", authactions.KubeAPIAuthDaemonSet)

	changed, err := tokenapi.WaitForLastUsedAtChange(subClient, clusterAuth, planted)
	require.NoError(k.T(), err, "The restarted %v should write lastUsedAt again", authactions.KubeAPIAuthDaemonSet)
	require.NoError(k.T(), tokenapi.VerifyLastUsedAtNear(changed, request.At), "lastUsedAt should hold the time of the review")

	require.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
		"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
}

func (k *KubeAPIAuthLastUsedAtSuite) TestAlternatingProxyAndClusterEndpointUseNeverMovesTheUpstreamTokenBack() {
	for _, category := range lastUsedAtCategories {
		k.Run(string(category), func() {
			subSession := k.session.NewSession()
			defer subSession.Cleanup()

			subClient, err := k.client.WithSession(subSession)
			require.NoError(k.T(), err, "Failed to scope the client to the test session")

			baseline, err := authactions.KubeAPIAuthLogs(subClient, k.clusterID)
			require.NoError(k.T(), err, "Failed to read what %v had logged", authactions.KubeAPIAuthDaemonSet)

			credential, err := tokenapi.NewClusterAuthCredential(subClient, k.clusterID, category)
			require.NoError(k.T(), err, "Failed to mint a %v token scoped to the cluster", category)

			upstream, err := tokenapi.RecordLastUsedAt(subClient, category, "", credential.Name)
			require.NoError(k.T(), err, "Failed to start recording the lastUsedAt of the upstream %v token", category)

			reviews, proxied, useErr := kubeapiauthactions.SendTokenReviewsAlongsideProxyRequests(k.reviewPods[0], k.requester, k.clusterID, credential, defaults.TenSecondTimeout, defaults.OneMinuteTimeout)

			writes, recordErr := upstream.StopWhenQuiet(3 * defaults.FiveSecondTimeout)

			require.NoError(k.T(), useErr, "Failed to use the token through both paths")

			assert.NoError(k.T(), recordErr, "The upstream token lastUsedAt should be read for the whole loop")
			assert.NoError(k.T(), kubeapiauthactions.VerifyAllReviewed(reviews), "Every review should authenticate the token")
			assert.NoError(k.T(), tokenapi.VerifyAllSucceeded(proxied), "Every request through the Rancher proxy should be accepted")
			assert.NoError(k.T(), tokenapi.VerifyWritesNeverMoveBack(writes[credential.Name]), "Two writers should not move the upstream lastUsedAt back")
			assert.GreaterOrEqual(k.T(), len(writes[credential.Name]), 1, "Using the token for a minute through both paths should write lastUsedAt")
			assert.LessOrEqual(k.T(), len(writes[credential.Name]), 4, "Rancher and kube-api-auth should each write about once a minute")

			assert.NoError(k.T(), kubeapiauthactions.VerifyNoKubeAPIAuthErrors(subClient, k.clusterID, baseline),
				"%v should log no failed write, forbidden or conflict", authactions.KubeAPIAuthDaemonSet)
		})
	}
}

func TestKubeAPIAuthLastUsedAtSuite(t *testing.T) {
	suite.Run(t, new(KubeAPIAuthLastUsedAtSuite))
}

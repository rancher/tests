//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package kubeapiauth

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/kubeapi/tokens/exttokens"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const activeClusterState = "active"

type KubeAPIAuthTokenReviewSuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	clusterID  string
	accessKey  string
	validToken string
	image      string
	echoes     bool
	restoreACE func() error
}

func (k *KubeAPIAuthTokenReviewSuite) SetupSuite() {
	k.session = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err, "Failed to create Rancher client")
	k.client = client

	logrus.Info("Getting cluster name from the config file")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(k.T(), clusterName, "Cluster name should be set")

	k.clusterID, err = clusters.GetClusterIDByName(k.client, clusterName)
	require.NoError(k.T(), err, "Error getting cluster ID for cluster: %s", clusterName)

	cluster, err := k.client.Management.Cluster.ByID(k.clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", k.clusterID)

	require.Equal(k.T(), activeClusterState, cluster.State,
		"Cluster [%v] is %v rather than %v; a TokenReview is sent through the downstream proxy, which answers only while the cluster is up",
		clusterName, cluster.State, activeClusterState)

	logrus.Infof("Making sure the authorized cluster endpoint is on, which is what deploys the %v daemon set", authactions.KubeAPIAuthDaemonSet)
	k.restoreACE, err = authactions.EnsureACEEnabled(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to enable the authorized cluster endpoint on cluster [%v]", clusterName)

	logrus.Info("Creating the user whose token kube-api-auth is asked to review, bound to the cluster so Rancher syncs a cluster auth token for them")
	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), cluster, nil)
	require.NoError(k.T(), err, "Failed to create the user the TokenReview tests sign in as")

	logrus.Info("Generating the authorized cluster endpoint kubeconfig, which is what mints the token kube-api-auth can resolve")
	k.validToken, err = authactions.ClusterAuthTokenFromKubeconfig(userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read a cluster auth token out of the generated kubeconfig")

	k.accessKey = strings.SplitN(k.validToken, ":", 2)[0]

	logrus.Infof("Waiting for Rancher to sync cluster auth token [%v] into the downstream cluster", k.accessKey)
	err = authactions.WaitForClusterAuthToken(k.client, k.clusterID, k.accessKey)
	require.NoError(k.T(), err, "kube-api-auth resolves tokens against the cluster auth tokens synced downstream, and [%v] never arrived", k.accessKey)

	k.image, err = authactions.KubeAPIAuthImage(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read the image the kube-api-auth daemon set runs")

	k.echoes, err = authactions.EchoesRequestAPIVersion(k.client, k.clusterID, k.validToken)
	require.NoError(k.T(), err, "Failed to ask kube-api-auth which apiVersion it answers in")

	logrus.Infof("Reviewing tokens against %v from a control plane node of cluster [%v], served by %v",
		authactions.KubeAPIAuthEndpoint, clusterName, k.image)

	if !k.echoes {
		logrus.Warnf("Image %v answers every TokenReview as %v rather than echoing the request, so it predates the fix for the apiVersion the request carried; the cases that cover that fix will be skipped",
			k.image, authactions.TokenReviewAPIV1Beta)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) skipOnPreFixImage() {
	if !k.echoes {
		k.T().Skipf("kube-api-auth image %v answers every TokenReview as %v and refuses with an empty body, so it predates the fix this case covers",
			k.image, authactions.TokenReviewAPIV1Beta)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TearDownSuite() {
	defer k.session.Cleanup()

	if k.restoreACE != nil {
		logrus.Info("Putting the authorized cluster endpoint back the way the suite found it")
		require.NoError(k.T(), k.restoreACE(), "Failed to restore the authorized cluster endpoint")
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewAcceptsVersionOne() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Reviewing a valid token with apiVersion %v", authactions.TokenReviewAPIV1)
	result, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, k.validToken)
	require.NoError(k.T(), err, "Failed to send the TokenReview")

	require.Equal(k.T(), 200, result.StatusCode, "A valid token should be reviewed, got body [%v]", result.Body)
	require.Equal(k.T(), authactions.TokenReviewAPIV1, result.APIVersion, "The reply should echo the apiVersion the request carried, otherwise the apiserver rejects it as an unknown conversion")
	require.True(k.T(), result.Authenticated, "A valid token should authenticate, got body [%v]", result.Body)
	require.NotEmpty(k.T(), result.Username, "The reply should name the Rancher user the token belongs to")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewAcceptsVersionOneBeta() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	logrus.Infof("Reviewing a valid token with apiVersion %v", authactions.TokenReviewAPIV1Beta)
	result, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1Beta, authactions.TokenReviewKind, k.validToken)
	require.NoError(k.T(), err, "Failed to send the TokenReview")

	require.Equal(k.T(), 200, result.StatusCode, "A valid token should be reviewed, got body [%v]", result.Body)
	require.Equal(k.T(), authactions.TokenReviewAPIV1Beta, result.APIVersion, "The reply should echo v1beta1 so that clusters on the default webhook version keep working")
	require.True(k.T(), result.Authenticated, "A valid token should authenticate, got body [%v]", result.Body)
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewDefaultsAnAbsentVersion() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	requests := []struct {
		description string
		payload     string
	}{
		{"an empty apiVersion", authactions.NewTokenReviewPayload("", authactions.TokenReviewKind, k.validToken)},
		{"no apiVersion key at all", authactions.NewTokenReviewPayloadWithoutAPIVersion(authactions.TokenReviewKind, k.validToken)},
	}

	for _, request := range requests {
		logrus.Infof("Reviewing a valid token in a request carrying %v", request.description)

		result, err := authactions.PostTokenReview(k.client, k.clusterID, request.payload)
		require.NoError(k.T(), err, "Failed to send the TokenReview carrying %v", request.description)

		require.Equal(k.T(), 200, result.StatusCode, "A request carrying %v should still be reviewed, got body [%v]", request.description, result.Body)
		require.Equal(k.T(), authactions.TokenReviewAPIV1Beta, result.APIVersion, "A request carrying %v should be answered as v1beta1, which keeps hand built requests working", request.description)
		require.True(k.T(), result.Authenticated, "A valid token should authenticate, got body [%v]", result.Body)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRejectsUnsupportedVersions() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	unsupported := []string{"authentication.k8s.io/v2", "authorization.k8s.io/v1", "foo"}

	for _, apiVersion := range unsupported {
		logrus.Infof("Reviewing a valid token with the unsupported apiVersion %v", apiVersion)

		baseline, err := authactions.KubeAPIAuthLogs(k.client, k.clusterID)
		require.NoError(k.T(), err, "Failed to read what kube-api-auth had logged before the request")

		result, err := authactions.ReviewToken(k.client, k.clusterID, apiVersion, authactions.TokenReviewKind, k.validToken)
		require.NoError(k.T(), err, "Failed to send the TokenReview")

		require.Equal(k.T(), 400, result.StatusCode, "apiVersion [%v] should be refused outright rather than answered, got body [%v]", apiVersion, result.Body)
		require.NoError(k.T(), authactions.VerifyKubeAPIAuthLogged(k.client, k.clusterID, baseline, apiVersion, "is not supported"),
			"kube-api-auth answers a refused request with an empty body, so its log is what should say the apiVersion is unsupported")
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesWrongSecret() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	wrongSecret := k.accessKey + ":wrongsecret"

	for _, apiVersion := range []string{authactions.TokenReviewAPIV1, authactions.TokenReviewAPIV1Beta} {
		logrus.Infof("Reviewing a token whose secret is wrong with apiVersion %v", apiVersion)
		result, err := authactions.ReviewToken(k.client, k.clusterID, apiVersion, authactions.TokenReviewKind, wrongSecret)
		require.NoError(k.T(), err, "Failed to send the TokenReview")

		require.Equal(k.T(), 200, result.StatusCode, "A refusal is still an answer, so it should carry 200, got body [%v]", result.Body)
		require.Equal(k.T(), apiVersion, result.APIVersion, "A refusal must echo the apiVersion too, otherwise the apiserver logs an unknown conversion instead of an auth failure")
		require.False(k.T(), result.Authenticated, "A wrong secret should not authenticate, got body [%v]", result.Body)
		require.NotEmpty(k.T(), result.Error, "The refusal should say why, got body [%v]", result.Body)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesUnknownAccessKey() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	for _, apiVersion := range []string{authactions.TokenReviewAPIV1, authactions.TokenReviewAPIV1Beta} {
		logrus.Infof("Reviewing a token whose access key belongs to nobody with apiVersion %v", apiVersion)
		result, err := authactions.ReviewToken(k.client, k.clusterID, apiVersion, authactions.TokenReviewKind, "nonexistent:secret")
		require.NoError(k.T(), err, "Failed to send the TokenReview")

		require.Equal(k.T(), 200, result.StatusCode, "An unknown access key should be refused rather than error, got body [%v]", result.Body)
		require.Equal(k.T(), apiVersion, result.APIVersion, "A refusal must echo the apiVersion the request carried")
		require.False(k.T(), result.Authenticated, "An unknown access key should not authenticate, got body [%v]", result.Body)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRejectsTokenWithoutSeparator() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	logrus.Info("Reviewing a token carrying no colon between the access key and the secret")

	baseline, err := authactions.KubeAPIAuthLogs(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what kube-api-auth had logged before the request")

	result, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, "nocolonhere")
	require.NoError(k.T(), err, "Failed to send the TokenReview")

	require.Equal(k.T(), 400, result.StatusCode, "A token that cannot be split should be refused outright, got body [%v]", result.Body)
	require.NoError(k.T(), authactions.VerifyKubeAPIAuthLogged(k.client, k.clusterID, baseline, "found 1 parts of token"),
		"kube-api-auth answers a refused request with an empty body, so its log is what should say the token could not be split")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRejectsMalformedRequests() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	malformed := []struct {
		description string
		payload     string
		reason      string
	}{
		{"an empty token", authactions.NewTokenReviewPayload(authactions.TokenReviewAPIV1, authactions.TokenReviewKind, ""), "missing Token"},
		{"a kind that is not TokenReview", authactions.NewTokenReviewPayload(authactions.TokenReviewAPIV1, "SubjectAccessReview", k.validToken), "TokenReview"},
		{"a body that is not JSON", "{invalid", "invalid character"},
		{"an empty body", "", "unexpected end of JSON input"},
	}

	for _, request := range malformed {
		logrus.Infof("Reviewing a request carrying %v", request.description)

		baseline, err := authactions.KubeAPIAuthLogs(k.client, k.clusterID)
		require.NoError(k.T(), err, "Failed to read what kube-api-auth had logged before the request")

		result, err := authactions.PostTokenReview(k.client, k.clusterID, request.payload)
		require.NoError(k.T(), err, "Failed to send the TokenReview carrying %v", request.description)

		require.Equal(k.T(), 400, result.StatusCode, "A request carrying %v should be refused outright, got body [%v]", request.description, result.Body)
		require.NoError(k.T(), authactions.VerifyKubeAPIAuthLogged(k.client, k.clusterID, baseline, request.reason),
			"kube-api-auth answers a refused request with an empty body, so its log is what should say %v was rejected", request.description)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesAnExtTokenWithNoClusterAuthToken() {
	k.skipOnPreFixImage()

	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	extToken, err := exttokens.CreateExtToken(k.client, 0)
	require.NoError(k.T(), err, "Failed to mint an ext token")

	bearer := authactions.ExtTokenPrefix + extToken.Name + ":" + extToken.Status.Value

	for _, apiVersion := range []string{authactions.TokenReviewAPIV1, authactions.TokenReviewAPIV1Beta} {
		logrus.Infof("Reviewing an ext token with apiVersion %v", apiVersion)

		result, err := authactions.ReviewToken(k.client, k.clusterID, apiVersion, authactions.TokenReviewKind, bearer)
		require.NoError(k.T(), err, "Failed to send the TokenReview")

		require.Equal(k.T(), 200, result.StatusCode, "An ext token should be reviewed rather than refused outright, got body [%v]", result.Body)
		require.Equal(k.T(), apiVersion, result.APIVersion, "A refusal must echo the apiVersion the request carried")
		require.False(k.T(), result.Authenticated, "An ext token that is not scoped to the cluster should not authenticate, got body [%v]", result.Body)
		require.Contains(k.T(), result.Error, extToken.Name,
			"kube-api-auth should drop the %v prefix and look the token up by its name, so the refusal names [%v]", authactions.ExtTokenPrefix, extToken.Name)
	}
}

func TestKubeAPIAuthTokenReviewSuite(t *testing.T) {
	suite.Run(t, new(KubeAPIAuthTokenReviewSuite))
}

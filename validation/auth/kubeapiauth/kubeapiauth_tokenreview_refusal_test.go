//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13 && !2.14 && !2.15

package kubeapiauth

import (
	"context"
	"net/http"
	"strings"

	"github.com/rancher/shepherd/extensions/defaults"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
)

const missingClusterAuthTokenReason = "clusterauthtokens.cluster.cattle.io"

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesAnExpiredToken() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	logrus.Infof("Minting a token scoped to the cluster that expires in %v", 3*defaults.OneMinuteTimeout)
	token, err := authactions.NewSyncedClusterScopedToken(subClient, k.clusterID, 3*defaults.OneMinuteTimeout)
	require.NoError(k.T(), err, "Failed to mint a short lived token")

	before, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, token)
	require.NoError(k.T(), err, "Failed to send the TokenReview")
	require.True(k.T(), before.Authenticated, "The token should authenticate until it expires, got status %d", before.StatusCode)

	require.NoError(k.T(), authactions.VerifyTokenReviewRefused(k.client, k.clusterID, token, "expired"), "The token should be refused with a TokenReview")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesADisabledToken() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	token, err := authactions.NewSyncedClusterScopedToken(subClient, k.clusterID, defaults.ThirtyMinuteTimeout)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	accessKey, _, _ := strings.Cut(token, ":")

	before, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, token)
	require.NoError(k.T(), err, "Failed to send the TokenReview")
	require.True(k.T(), before.Authenticated, "The token should authenticate while it is enabled, got status %d", before.StatusCode)

	logrus.Infof("Disabling token [%v]", accessKey)
	_, err = k.client.WranglerContext.Mgmt.Token().Patch(accessKey, types.MergePatchType, []byte(`{"enabled":false}`))
	require.NoError(k.T(), err, "Failed to disable the token")

	require.NoError(k.T(), authactions.VerifyTokenReviewRefused(k.client, k.clusterID, token, "token is not enabled"), "The token should be refused with a TokenReview")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesADeletedToken() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	token, err := authactions.NewSyncedClusterScopedToken(subClient, k.clusterID, defaults.ThirtyMinuteTimeout)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	accessKey, _, _ := strings.Cut(token, ":")

	before, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, token)
	require.NoError(k.T(), err, "Failed to send the TokenReview")
	require.True(k.T(), before.Authenticated, "The token should authenticate before it is deleted, got status %d", before.StatusCode)

	logrus.Infof("Deleting token [%v]", accessKey)
	require.NoError(k.T(), k.client.WranglerContext.Mgmt.Token().Delete(accessKey, &metav1.DeleteOptions{}), "Failed to delete the token")
	require.NoError(k.T(), authactions.WaitForClusterAuthTokenGone(k.client, k.clusterID, accessKey), "Rancher should withdraw the cluster auth token of a deleted token")

	require.NoError(k.T(), authactions.VerifyTokenReviewRefused(k.client, k.clusterID, token, missingClusterAuthTokenReason), "The token should be refused with a TokenReview")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusesADisabledUser() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	cluster, err := k.client.Management.Cluster.ByID(k.clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", k.clusterID)

	user, userClient, err := rbac.AddUserWithRoleToCluster(subClient, rbac.StandardUser.String(), rbac.ClusterOwner.String(), cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	token, err := authactions.ClusterAuthTokenFromKubeconfig(userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to read a cluster auth token out of the generated kubeconfig")

	accessKey, _, _ := strings.Cut(token, ":")
	require.NoError(k.T(), authactions.WaitForClusterAuthToken(k.client, k.clusterID, accessKey), "The cluster auth token never reached the cluster")

	before, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, token)
	require.NoError(k.T(), err, "Failed to send the TokenReview")
	require.True(k.T(), before.Authenticated, "The user's token should authenticate while they are enabled, got status %d", before.StatusCode)

	logrus.Infof("Deactivating user [%v]", user.ID)
	require.NoError(k.T(), authactions.SetUserEnabled(k.client, user.ID, false), "Failed to deactivate the user")

	require.NoError(k.T(), authactions.VerifyTokenReviewRefused(k.client, k.clusterID, token, "user is not enabled"), "The token should be refused with a TokenReview")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewAnswersInJSON() {
	answers := []struct {
		description string
		payload     string
		status      int
	}{
		{"a valid token", authactions.NewTokenReviewPayload(authactions.TokenReviewAPIV1, authactions.TokenReviewKind, k.validToken), http.StatusOK},
		{"a wrong secret", authactions.NewTokenReviewPayload(authactions.TokenReviewAPIV1, authactions.TokenReviewKind, k.accessKey+":wrongsecret"), http.StatusOK},
		{"an unknown access key", authactions.NewTokenReviewPayload(authactions.TokenReviewAPIV1, authactions.TokenReviewKind, "nonexistent:secret"), http.StatusOK},
		{"a body that is not JSON", "{invalid", http.StatusBadRequest},
	}

	for _, answer := range answers {
		logrus.Infof("Reviewing a request carrying %v", answer.description)

		result, err := authactions.PostTokenReview(k.client, k.clusterID, answer.payload)
		require.NoError(k.T(), err, "Failed to send the TokenReview carrying %v", answer.description)

		require.Equal(k.T(), answer.status, result.StatusCode, "A request carrying %v was answered with the wrong status", answer.description)
		require.Contains(k.T(), result.ContentType, runtime.ContentTypeJSON, "The answer to a request carrying %v should say it is JSON rather than leave Go to sniff it", answer.description)
	}
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewLogsARefusalOnceAtInfoLevel() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	token, err := authactions.NewSyncedClusterScopedToken(subClient, k.clusterID, defaults.ThirtyMinuteTimeout)
	require.NoError(k.T(), err, "Failed to mint a token scoped to the cluster")

	accessKey, _, _ := strings.Cut(token, ":")

	baseline, err := authactions.KubeAPIAuthLogs(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read the %v logs before the review", authactions.KubeAPIAuthDaemonSet)

	logrus.Infof("Reviewing token [%v] with a wrong secret", accessKey)
	result, err := authactions.ReviewToken(k.client, k.clusterID, authactions.TokenReviewAPIV1, authactions.TokenReviewKind, accessKey+":wrongsecret")
	require.NoError(k.T(), err, "Failed to send the TokenReview")
	require.False(k.T(), result.Authenticated, "A wrong secret should not authenticate, got status %d", result.StatusCode)

	require.NoError(k.T(), authactions.VerifyKubeAPIAuthRefused(k.client, k.clusterID, baseline, accessKey, "secretKey hash does not match"),
		"A refusal is routine, so kube-api-auth should log it once as info and never as an error")
}

func (k *KubeAPIAuthTokenReviewSuite) TestTokenReviewRefusalThroughTheAuthorizedClusterEndpointIsLoggedOnce() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subClient, err := k.client.WithSession(subSession)
	require.NoError(k.T(), err, "Failed to scope the client to the test session")

	cluster, err := k.client.Management.Cluster.ByID(k.clusterID)
	require.NoError(k.T(), err, "Failed to retrieve cluster by ID: %s", k.clusterID)

	_, userClient, err := rbac.AddUserWithRoleToCluster(subClient, rbac.StandardUser.String(), rbac.ClusterOwner.String(), cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	generated, err := authactions.SyncedACEKubeconfig(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to generate the kubeconfig the authorized cluster endpoint is reached with")

	restConfigs, err := authactions.ACERestConfigs(generated)
	require.NoError(k.T(), err, "Failed to read the authorized cluster endpoint contexts out of the kubeconfig")
	require.NotEmpty(k.T(), restConfigs, "The kubeconfig should name an authorized cluster endpoint context")

	for name := range restConfigs {
		accessKey := namegen.AppendRandomString("kubeconfig-u-nosuchuser")

		clientsets, err := authactions.ACEClientsetsWithToken(generated, accessKey+":secret")
		require.NoError(k.T(), err, "Failed to build a client that authenticates with an unknown token")

		baseline, err := authactions.KubeAPIAuthLogs(k.client, k.clusterID)
		require.NoError(k.T(), err, "Failed to read the %v logs before the request", authactions.KubeAPIAuthDaemonSet)

		logrus.Infof("Reaching the cluster with unknown token [%v] through context [%v]", accessKey, name)

		_, err = clientsets[name].CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.Error(k.T(), err, "An unknown token should be refused by the authorized cluster endpoint context [%v]", name)
		require.True(k.T(), apierrors.IsUnauthorized(err), "The refusal through context [%v] should be Unauthorized rather than anything else, got: %v", name, err)

		require.NoError(k.T(), authactions.VerifyKubeAPIAuthRefused(k.client, k.clusterID, baseline, accessKey, missingClusterAuthTokenReason),
			"The kube-apiserver behind context [%v] should hand a refused token to kube-api-auth once and get a refusal rather than a failed call", name)
	}
}

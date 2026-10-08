//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package kubeapiauth

import (
	"context"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	authnv1 "k8s.io/api/authentication/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

const (
	deleteVerb    = "delete"
	nodesResource = "nodes"
)

type KubeAPIAuthACESuite struct {
	suite.Suite
	session    *session.Session
	client     *rancher.Client
	clusterID  string
	cluster    *management.Cluster
	restoreACE func() error
}

func (k *KubeAPIAuthACESuite) SetupSuite() {
	k.session = session.NewSession()

	client, err := rancher.NewClient("", k.session)
	require.NoError(k.T(), err, "Failed to create Rancher client")
	k.client = client

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
}

func (k *KubeAPIAuthACESuite) TearDownSuite() {
	defer k.session.Cleanup()

	if k.restoreACE != nil {
		logrus.Info("Putting the authorized cluster endpoint back the way the suite found it")
		require.NoError(k.T(), k.restoreACE(), "Failed to restore the authorized cluster endpoint")
	}
}

func (k *KubeAPIAuthACESuite) TestACELoginAsClusterOwner() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	for name, clientset := range clientsets {
		logrus.Infof("Listing namespaces through the authorized cluster endpoint context [%v]", name)

		_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.NoError(k.T(), err, "A cluster owner should reach the cluster through the authorized cluster endpoint context [%v]", name)
	}
}

func (k *KubeAPIAuthACESuite) TestACEResolvesTheRancherIdentity() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	for name, clientset := range clientsets {
		logrus.Infof("Asking the cluster who it thinks it is talking to through context [%v]", name)

		review, err := clientset.AuthenticationV1().SelfSubjectReviews().Create(context.Background(), &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
		require.NoError(k.T(), err, "The cluster should resolve the identity behind the authorized cluster endpoint context [%v]", name)
		require.Equal(k.T(), userClient.UserID, review.Status.UserInfo.Username,
			"kube-api-auth should resolve the token to the Rancher user it belongs to, so that bindings written against that user apply")
	}
}

func (k *KubeAPIAuthACESuite) TestACEHonoursTheRancherRole() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, ownerClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	_, memberClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterMember.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterMember)

	ownerClientsets, err := authactions.ACEClientsets(k.client, ownerClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts of the cluster owner")

	for name, clientset := range ownerClientsets {
		logrus.Infof("Checking what a cluster owner may do through context [%v]", name)

		allowed, err := authactions.CanI(clientset, deleteVerb, nodesResource)
		require.NoError(k.T(), err, "Failed to ask what the cluster owner may do through context [%v]", name)
		require.True(k.T(), allowed, "A cluster owner should be allowed to delete nodes through the authorized cluster endpoint")
	}

	memberClientsets, err := authactions.ACEClientsets(k.client, memberClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts of the cluster member")

	for name, clientset := range memberClientsets {
		logrus.Infof("Checking what a cluster member may do through context [%v]", name)

		allowed, err := authactions.CanI(clientset, deleteVerb, nodesResource)
		require.NoError(k.T(), err, "Failed to ask what the cluster member may do through context [%v]", name)
		require.False(k.T(), allowed,
			"A cluster member should not be allowed to delete nodes through the authorized cluster endpoint, so the Rancher role is what governs access rather than the token alone")
	}
}

func (k *KubeAPIAuthACESuite) TestACERejectsATamperedToken() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	kubeconfig, err := authactions.SyncedACEKubeconfig(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to generate the kubeconfig the authorized cluster endpoint is reached with")

	restConfigs, err := authactions.ACERestConfigs(kubeconfig)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	for name, restConfig := range restConfigs {
		tampered := rest.CopyConfig(restConfig)
		tampered.BearerToken = restConfig.BearerToken + "tampered"

		clientset, err := kubernetes.NewForConfig(tampered)
		require.NoError(k.T(), err, "Failed to build a clientset for the %v context", name)

		logrus.Infof("Reaching the cluster with a tampered token through context [%v]", name)

		_, err = clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.Error(k.T(), err, "A tampered token should be refused by the authorized cluster endpoint context [%v]", name)
		require.True(k.T(), apierrors.IsUnauthorized(err), "The refusal should be Unauthorized rather than anything else, got: %v", err)
	}
}

func TestKubeAPIAuthACESuite(t *testing.T) {
	suite.Run(t, new(KubeAPIAuthACESuite))
}

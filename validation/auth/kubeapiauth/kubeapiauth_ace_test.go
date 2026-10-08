//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress

package kubeapiauth

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	"github.com/rancher/shepherd/pkg/session"
	authactions "github.com/rancher/tests/actions/auth"
	projectsapi "github.com/rancher/tests/actions/kubeapi/projects"
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

	shortTokenLifetime = 90 * time.Second
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

func (k *KubeAPIAuthACESuite) TestACEServesOneContextPerControlPlaneNode() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	fqdn, err := authactions.ACEFQDN(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read the address the authorized cluster endpoint is published under")

	if fqdn != "" {
		k.T().Skipf("The authorized cluster endpoint is published under %v, so the kubeconfig names that one address rather than one context per control plane node", fqdn)
	}

	controlPlanes, err := authactions.ControlPlaneNodeCount(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to count the control plane nodes of the cluster")
	require.NotZero(k.T(), controlPlanes, "A cluster serving the authorized cluster endpoint must run at least one control plane node")

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	kubeconfig, err := authactions.SyncedACEKubeconfig(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to generate the kubeconfig the authorized cluster endpoint is reached with")

	restConfigs, err := authactions.ACERestConfigs(kubeconfig)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	logrus.Infof("Cluster runs %v control plane nodes and the generated kubeconfig names %v authorized cluster endpoint contexts", controlPlanes, len(restConfigs))
	require.Len(k.T(), restConfigs, controlPlanes,
		"The kubeconfig should name one authorized cluster endpoint context per control plane node, otherwise the tests that loop over the contexts cover fewer nodes than the cluster has")
}

func (k *KubeAPIAuthACESuite) TestACEReachesTheClusterThroughTheRancherProxy() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	kubeconfig, err := authactions.SyncedACEKubeconfig(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to generate the kubeconfig the authorized cluster endpoint is reached with")

	restConfig, err := authactions.ProxyRestConfig(kubeconfig)
	require.NoError(k.T(), err, "Failed to build a client for the Rancher proxy context")

	clientset, err := kubernetes.NewForConfig(restConfig)
	require.NoError(k.T(), err, "Failed to build a clientset for the Rancher proxy context")

	logrus.Info("Listing namespaces through the Rancher proxy context, which the authorized cluster endpoint must not have displaced")

	_, err = clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
	require.NoError(k.T(), err, "Turning the authorized cluster endpoint on must leave the Rancher proxy context working, since that is how most clients still reach the cluster")
}

func (k *KubeAPIAuthACESuite) TestACEHonoursReadOnlyAndProjectMemberRoles() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	project, _, err := projectsapi.CreateProjectAndNamespace(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to create the project the project scoped roles are granted in")

	for _, role := range []rbac.Role{rbac.ReadOnly, rbac.ProjectMember} {
		_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), role.String(), k.cluster, project)
		require.NoError(k.T(), err, "Failed to create a user bound to the project as %v", role)

		clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
		require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts of a %v", role)

		for name, clientset := range clientsets {
			logrus.Infof("Checking what a %v may do through context [%v]", role, name)

			allowed, err := authactions.CanI(clientset, deleteVerb, nodesResource)
			require.NoError(k.T(), err, "Failed to ask what a %v may do through context [%v]", role, name)
			require.False(k.T(), allowed, "A %v should not be allowed to delete nodes through the authorized cluster endpoint", role)
		}
	}
}

func (k *KubeAPIAuthACESuite) TestACERejectsADeletedToken() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	kubeconfig, err := authactions.SyncedACEKubeconfig(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to generate the kubeconfig the authorized cluster endpoint is reached with")

	accessKey, err := authactions.AccessKeyFromKubeconfig(kubeconfig)
	require.NoError(k.T(), err, "Failed to read the access key out of the generated kubeconfig")

	clientsets, err := authactions.ClientsetsFromKubeconfig(kubeconfig)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	for name, clientset := range clientsets {
		_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.NoError(k.T(), err, "The token should work through context [%v] before it is deleted", name)
	}

	logrus.Infof("Deleting token [%v], which is the token the authorized cluster endpoint kubeconfig carries", accessKey)

	token, err := userClient.Management.Token.ByID(accessKey)
	require.NoError(k.T(), err, "Failed to retrieve token [%v]; the token API only answers for the user that owns the token", accessKey)
	require.NoError(k.T(), userClient.Management.Token.Delete(token), "Failed to delete token [%v]", accessKey)

	logrus.Infof("Waiting for Rancher to withdraw cluster auth token [%v] from the downstream cluster", accessKey)
	require.NoError(k.T(), authactions.WaitForClusterAuthTokenGone(k.client, k.clusterID, accessKey),
		"Deleting a token must withdraw the cluster auth token it was synced as, otherwise the authorized cluster endpoint keeps honouring a revoked credential")

	for name, clientset := range clientsets {
		logrus.Infof("Waiting for context [%v] to refuse the deleted token, which takes until the kube-apiserver's webhook answer cache expires", name)

		require.NoError(k.T(), authactions.WaitForUnauthorized(clientset),
			"A deleted token should stop reaching the cluster through context [%v] once the kube-apiserver stops serving its cached answer", name)
	}
}

func (k *KubeAPIAuthACESuite) TestACEDisableRemovesTheDaemonSetAndEnableBringsItBack() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	subSession.RegisterCleanupFunc(func() error {
		if err := authactions.SetACEEnabled(k.client, k.clusterID, true); err != nil {
			return err
		}

		_, err := authactions.EnsureACEEnabled(k.client, k.clusterID)

		return err
	})

	logrus.Infof("Turning the authorized cluster endpoint off, which should take the %v daemon set with it", authactions.KubeAPIAuthDaemonSet)
	require.NoError(k.T(), authactions.SetACEEnabled(k.client, k.clusterID, false), "Failed to turn the authorized cluster endpoint off")

	require.NoError(k.T(), daemonsets.WaitForDaemonSetDeletion(k.client, k.clusterID, authactions.KubeAPIAuthNamespace, authactions.KubeAPIAuthDaemonSet),
		"Turning the authorized cluster endpoint off should remove the %v daemon set, otherwise the cluster keeps answering token reviews it no longer serves", authactions.KubeAPIAuthDaemonSet)

	enabled, err := authactions.ACEEnabled(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read back the authorized cluster endpoint setting")
	require.False(k.T(), enabled, "The authorized cluster endpoint should read back as off")

	logrus.Info("Turning the authorized cluster endpoint back on")
	require.NoError(k.T(), authactions.SetACEEnabled(k.client, k.clusterID, true), "Failed to turn the authorized cluster endpoint back on")

	_, err = authactions.EnsureACEEnabled(k.client, k.clusterID)
	require.NoError(k.T(), err, "The %v daemon set should come back ready once the authorized cluster endpoint is on again", authactions.KubeAPIAuthDaemonSet)

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts after the toggle")

	for name, clientset := range clientsets {
		logrus.Infof("Waiting for context [%v] to accept a login again after the authorized cluster endpoint was turned off and on", name)

		require.NoError(k.T(), authactions.WaitForAuthorized(clientset),
			"A cluster owner should reach the cluster through context [%v] once the authorized cluster endpoint is back", name)
	}
}

func (k *KubeAPIAuthACESuite) TestACELeavesNoUnknownConversionInTheAPIServerLog() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	exposed, err := authactions.APIServerPodsExposed(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to look for the kube-apiserver pods")

	if !exposed {
		k.T().Skip("This distribution serves the Kubernetes API from the host process rather than a pod, so the flags it runs with and the log it writes cannot be read from the cluster")
	}

	webhookVersion, err := authactions.APIServerWebhookVersion(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read the TokenReview version the kube-apiserver sends")

	logrus.Infof("The kube-apiserver sends TokenReviews as [%v], where an empty value means the Kubernetes default of %v",
		webhookVersion, authactions.TokenReviewAPIV1Beta)

	baseline, err := authactions.APIServerLogs(k.client, k.clusterID)
	require.NoError(k.T(), err, "Failed to read what the kube-apiserver had logged before the login")

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	for name, clientset := range clientsets {
		_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.NoError(k.T(), err, "A cluster owner should reach the cluster through context [%v]", name)
	}

	require.NoError(k.T(), authactions.VerifyNoUnknownConversion(k.client, k.clusterID, baseline),
		"kube-api-auth must answer in the version the kube-apiserver asked in, so a login should leave no conversion failure behind")
}

func (k *KubeAPIAuthACESuite) TestACERejectsADisabledUser() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	user, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	clientsets, err := authactions.ACEClientsets(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to build a client for the authorized cluster endpoint contexts")

	for name, clientset := range clientsets {
		_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.NoError(k.T(), err, "The user should reach the cluster through context [%v] while they are still enabled", name)
	}

	subSession.RegisterCleanupFunc(func() error {
		return authactions.SetUserEnabled(k.client, user.ID, true)
	})

	logrus.Infof("Deactivating user [%v], whose token the authorized cluster endpoint kubeconfig carries", user.ID)
	require.NoError(k.T(), authactions.SetUserEnabled(k.client, user.ID, false), "Failed to deactivate user [%v]", user.ID)

	for name, clientset := range clientsets {
		logrus.Infof("Waiting for context [%v] to refuse the deactivated user", name)

		require.NoError(k.T(), authactions.WaitForUnauthorized(clientset),
			"A deactivated user should stop reaching the cluster through context [%v]; their token must not outlive the account it belongs to", name)
	}
}

func (k *KubeAPIAuthACESuite) TestACERejectsAnExpiredToken() {
	subSession := k.session.NewSession()
	defer subSession.Cleanup()

	_, userClient, err := rbac.AddUserWithRoleToCluster(k.client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), k.cluster, nil)
	require.NoError(k.T(), err, "Failed to create a user bound to the cluster as %v", rbac.ClusterOwner)

	kubeconfig, err := authactions.SyncedACEKubeconfig(k.client, userClient, k.clusterID)
	require.NoError(k.T(), err, "Failed to generate the kubeconfig the authorized cluster endpoint is reached with")

	logrus.Infof("Minting a token scoped to the cluster that expires in %v", shortTokenLifetime)
	shortToken, err := authactions.NewClusterScopedToken(userClient, k.clusterID, shortTokenLifetime)
	require.NoError(k.T(), err, "Failed to mint a short lived token")

	accessKey := strings.SplitN(shortToken, ":", 2)[0]
	require.NoError(k.T(), authactions.WaitForClusterAuthToken(k.client, k.clusterID, accessKey),
		"kube-api-auth resolves tokens against the cluster auth tokens synced downstream, and [%v] never arrived", accessKey)

	clientsets, err := authactions.ACEClientsetsWithToken(kubeconfig, shortToken)
	require.NoError(k.T(), err, "Failed to build a client that authenticates with the short lived token")

	for name, clientset := range clientsets {
		_, err := clientset.CoreV1().Namespaces().List(context.Background(), metav1.ListOptions{})
		require.NoError(k.T(), err, "The short lived token should reach the cluster through context [%v] before it expires", name)
	}

	for name, clientset := range clientsets {
		logrus.Infof("Waiting for context [%v] to refuse the token once it has expired", name)

		require.NoError(k.T(), authactions.WaitForUnauthorized(clientset),
			"An expired token should stop reaching the cluster through context [%v]", name)
	}
}

func TestKubeAPIAuthACESuite(t *testing.T) {
	suite.Run(t, new(KubeAPIAuthACESuite))
}

//go:build (validation || infra.any || cluster.any || extended) && !sanity && !stress && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package kubeconfigs

import (
	"context"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/session"
	kubeconfigapi "github.com/rancher/tests/actions/kubeconfigs"
	watchlistapi "github.com/rancher/tests/actions/watchlist"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ExtKubeconfigWatchListTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	cluster            *management.Cluster
}

func (w *ExtKubeconfigWatchListTestSuite) TearDownSuite() {
	w.session.Cleanup()
}

func (w *ExtKubeconfigWatchListTestSuite) SetupSuite() {
	w.session = session.NewSession()

	client, err := rancher.NewClient("", w.session)
	require.NoError(w.T(), err)
	w.client = client

	log.Info("Getting cluster name from the config file and append cluster details in the struct.")
	clusterName := client.RancherConfig.ClusterName
	require.NotEmptyf(w.T(), clusterName, "Cluster name to install should be set")
	clusterID, err := extensionscluster.GetClusterIDByName(w.client, clusterName)
	require.NoError(w.T(), err, "Error getting cluster ID")
	w.cluster, err = w.client.Management.Cluster.ByID(clusterID)
	require.NoError(w.T(), err)
}

func (w *ExtKubeconfigWatchListTestSuite) TestWatchListForKubeconfigs() {
	subSession := w.session.NewSession()
	defer subSession.Cleanup()

	log.Infof("As user %s, creating a kubeconfig for cluster: %s", w.client.UserID, w.cluster.ID)
	createdKubeconfig, err := kubeconfigapi.CreateKubeconfig(w.client, []string{w.cluster.ID}, "", nil)
	require.NoError(w.T(), err)
	require.NotEmpty(w.T(), createdKubeconfig.Status.Value)

	log.Info("Starting watch on kubeconfigs")
	sendInitial := true
	watcher, err := w.client.WranglerContext.Ext.Kubeconfig().Watch(metav1.ListOptions{
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

func TestKubeconfigWatchListTestSuite(w *testing.T) {
	suite.Run(w, new(ExtKubeconfigWatchListTestSuite))
}


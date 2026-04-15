//go:build (validation || infra.any || cluster.any || stress) && !sanity && !extended

package charts

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/tests/actions/charts"

	"github.com/rancher/shepherd/pkg/session"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type WebhookTestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	clusterName  string
	chartVersion string
}

func (w *WebhookTestSuite) TearDownSuite() {
	w.session.Cleanup()
}

func (w *WebhookTestSuite) SetupSuite() {
	testSession := session.NewSession()
	w.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(w.T(), err)

	w.client = client

	// Get clusterName from config yaml
	w.clusterName = client.RancherConfig.ClusterName
	w.chartVersion, err = client.Catalog.GetLatestChartVersion(charts.RancherWebhookName, catalog.RancherChartRepo)
	require.NoError(w.T(), err)
}

func (w *WebhookTestSuite) TestWebhookChart() {
	subSession := w.session.NewSession()
	defer subSession.Cleanup()

	tests := []struct {
		cluster string
	}{
		{localCluster},
		{w.clusterName},
	}

	for _, tt := range tests {

		clusterID, err := clusters.GetClusterIDByName(w.client, tt.cluster)
		require.NoError(w.T(), err)

		w.Run("Verify the version of webhook on "+tt.cluster, func() {
			subSession := w.session.NewSession()
			defer subSession.Cleanup()

			initialWebhookChart, err := extencharts.GetChartStatus(w.client, clusterID, charts.RancherWebhookNamespace, charts.RancherWebhookName)
			require.NoError(w.T(), err)
			chartVersion := initialWebhookChart.ChartDetails.Spec.Chart.Metadata.Version
			require.NoError(w.T(), err)
			assert.Equal(w.T(), w.chartVersion, chartVersion)
		})

		w.Run("Verify webhook pod logs on "+tt.cluster, func() {
			_, podName, err := getWebhookPodSpec(w.client, clusterID)
			require.NoError(w.T(), err)

			podLogs, err := kubeconfig.GetPodLogs(w.client, clusterID, podName, charts.RancherWebhookNamespace, "")
			require.NoError(w.T(), err)
			webhookLogs := validateWebhookPodLogs(podLogs)
			require.Nil(w.T(), webhookLogs)
		})

		w.Run("Verify webhook securityContext on "+tt.cluster, func() {
			podSpec, _, err := getWebhookPodSpec(w.client, clusterID)
			require.NoError(w.T(), err)

			err = validateWebhookPodSecurityContext(podSpec)
			require.NoError(w.T(), err)
		})

		w.Run("Verify the count of webhook is greater than zero and list webhooks on "+tt.cluster, func() {
			webhookList, err := getWebhookNames(w.client, clusterID, resourceName)
			require.NoError(w.T(), err)

			assert.True(w.T(), len(webhookList) > 0, "Expected webhooks list to be greater than zero")
			log.Info("Count of webhook obtained for the cluster: ", tt.cluster, " is ", len(webhookList))
			listStr := strings.Join(webhookList, ", ")
			log.WithField("", listStr).Info("List of webhooks obtained for the ", tt.cluster)
		})
	}
}

func TestWebhookTestSuite(t *testing.T) {
	suite.Run(t, new(WebhookTestSuite))
}

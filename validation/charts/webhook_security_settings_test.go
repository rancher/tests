//go:build (validation || infra.any || cluster.any || stress) && !sanity && !extended && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package charts

import (
	"testing"

	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type WebhookSecuritySettingsTestSuite struct {
	webhookSuiteBase
}

func (w *WebhookSecuritySettingsTestSuite) TestWebhookSecuritySettings() {
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

		w.Run("Verify webhook securityContext on "+tt.cluster, func() {
			podSpec, _, err := getWebhookPodSpec(w.client, clusterID)
			require.NoError(w.T(), err)

			err = validateWebhookPodSecurityContext(podSpec)
			require.NoError(w.T(), err)
		})
	}
}

func TestWebhookSecuritySettingsTestSuite(t *testing.T) {
	suite.Run(t, new(WebhookSecuritySettingsTestSuite))
}

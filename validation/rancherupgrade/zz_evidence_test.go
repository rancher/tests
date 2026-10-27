//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

// Throwaway evidence harness: dumps both chart namespaces' state and the
// Rancher server pods' recent logs through the proxied k8s client only, so it
// works against degraded envs whose management API no longer answers. Only
// ever dispatched explicitly by name; delete before final review.
package rancherupgrade

import (
	"context"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/session"
	interoperableCharts "github.com/rancher/tests/interoperability/charts"
	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	corev1 "k8s.io/api/core/v1"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type ObservabilityEvidenceSuite struct {
	suite.Suite
}

func (s *ObservabilityEvidenceSuite) TestDumpEvidence() {
	testSession := session.NewSession()
	defer testSession.Cleanup()

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)

	k8s, err := clusterK8sClient(client, localClusterName)
	require.NoError(s.T(), err)

	logNamespaceWorkloadState(k8s, interoperableCharts.StackStateServerNamespace)
	logNamespaceWorkloadState(k8s, interoperableCharts.StackstateNamespace)

	tail := int64(rancherLogTailLines)
	pods, err := k8s.CoreV1().Pods(cattleSystemNamespace).List(context.TODO(), meta.ListOptions{LabelSelector: rancherPodLabel})
	if err != nil {
		log.Warnf("diagnostics: listing rancher pods failed: %v", err)
		return
	}
	for i := range pods.Items {
		logs, logErr := k8s.CoreV1().Pods(cattleSystemNamespace).GetLogs(pods.Items[i].Name, &corev1.PodLogOptions{TailLines: &tail}).DoRaw(context.TODO())
		if logErr != nil {
			log.Warnf("diagnostics: fetching rancher logs from %s failed: %v", pods.Items[i].Name, logErr)
			continue
		}
		log.Infof("diagnostics: rancher server log tail from pod %s:\n%s", pods.Items[i].Name, string(logs))
	}
}

func TestObservabilityEvidenceSuite(t *testing.T) {
	suite.Run(t, new(ObservabilityEvidenceSuite))
}

//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

// Throwaway evidence harness: dumps the live suse-observability namespace
// state (pods, crash terminations, previous-container logs, PVCs, workloads)
// through the proxied k8s client only, so it still works against a degraded
// env whose management API no longer answers. Only ever dispatched explicitly
// by name; delete before final review.
package rancherupgrade

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/session"
	interoperableCharts "github.com/rancher/tests/interoperability/charts"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
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
}

func TestObservabilityEvidenceSuite(t *testing.T) {
	suite.Run(t, new(ObservabilityEvidenceSuite))
}

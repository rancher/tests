//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

// Throwaway evidence harness: dumps the live suse-observability namespace
// state (pods, waiting reasons, jobs, PVCs, workloads) without touching the
// release, so a failed install's culprit can be read from one freeform run
// instead of burning another 30-minute helm attempt. Only ever dispatched
// explicitly by name; delete before final review.
package rancherupgrade

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/session"
	actionsClusters "github.com/rancher/tests/actions/clusters"
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

	localCluster, err := actionsClusters.GetClusterByName(client, localClusterName)
	require.NoError(s.T(), err)
	catalogClient, err := client.GetClusterCatalogClient(localCluster.ID)
	require.NoError(s.T(), err)

	logInstallDiagnostics(client, catalogClient, localCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo)
}

func TestObservabilityEvidenceSuite(t *testing.T) {
	suite.Run(t, new(ObservabilityEvidenceSuite))
}

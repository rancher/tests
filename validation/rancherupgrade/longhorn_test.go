//go:build validation && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/storage"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LonghornUpgradeTestSuite struct {
	suite.Suite
	session *session.Session
	client  *rancher.Client
	cluster *clusters.ClusterMeta
}

const (
	// checksumWorkloadName is the deterministic name shared by the PVC and the nginx
	// deployment created in the pre-upgrade phase and relocated by later phases.
	checksumWorkloadName = "rancher-upgrade-longhorn"

	// checksumContent is the shell-safe data written to the checksum file on the
	// Longhorn-backed volume before the Rancher upgrade and verified after it.
	checksumContent = "rancher-upgrade-longhorn-checksum-v1"
)

// loadUpgradeInput returns the rancherUpgradeInput section of the test config.
func loadUpgradeInput() *UpgradeInput {
	upgradeInput := new(UpgradeInput)
	config.LoadConfig(UpgradeInputConfigKey, upgradeInput)

	return upgradeInput
}

func (s *LonghornUpgradeTestSuite) SetupSuite() {
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)
	s.client = client

	s.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
}

// TearDownSuite deliberately does not call session.Cleanup(): resources created in
// the pre-upgrade phase (Longhorn chart, PVC, workload) must survive into the
// post-upgrade and chart-upgrade phases, which run as separate go test processes.
// The pipeline environment teardown is the cleanup for these resources.
func (s *LonghornUpgradeTestSuite) TearDownSuite() {
}

func (s *LonghornUpgradeTestSuite) TestLonghornPreUpgrade() {
	project, _, err := projects.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	// Resolve the version from the cluster-scoped catalog and install that: the
	// management view can offer versions the cluster-scoped shard does not serve
	// yet (rancher-2.15-catalog-sharding-issue.md), and the chart action validates
	// against the cluster-scoped index. The pre-phase still yields the 2.14-era
	// version by construction, since it runs against the pre-upgrade Rancher.
	catalogClient, err := s.client.GetClusterCatalogClient(s.cluster.ID)
	require.NoError(s.T(), err)
	version, err := catalogClient.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
	require.NoError(s.T(), err)

	err = charts.InstallLonghornChart(s.client, charts.PayloadOpts{
		Namespace: charts.LonghornNamespace,
		Host:      s.client.RancherConfig.Host,
		InstallOptions: charts.InstallOptions{
			Cluster:   s.cluster,
			Version:   version,
			ProjectID: project.ID,
		},
	}, nil)
	require.NoError(s.T(), err)

	err = storage.CreateChecksumWorkload(s.client, s.cluster.ID, charts.LonghornStorageClass, checksumWorkloadName, checksumContent)
	require.NoError(s.T(), err)
}

func (s *LonghornUpgradeTestSuite) TestLonghornPostUpgrade() {
	cfg := loadUpgradeInput()
	require.NotEmpty(s.T(), cfg.TargetVersion, "rancherUpgradeInput.targetVersion must be set for post-upgrade phases")

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)

	chart, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(s.T(), err)
	require.True(s.T(), chart.IsAlreadyInstalled, "longhorn chart must still be installed after the Rancher server upgrade")

	err = storage.VerifyChecksumWorkload(s.client, s.cluster.ID, checksumWorkloadName, checksumContent)
	require.NoError(s.T(), err)
}

func (s *LonghornUpgradeTestSuite) TestLonghornChartUpgrade() {
	cfg := loadUpgradeInput()
	require.NotEmpty(s.T(), cfg.TargetVersion, "rancherUpgradeInput.targetVersion must be set for post-upgrade phases")

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)

	// Resolve the latest version from the cluster-scoped catalog: the management
	// view can report versions absent from the cluster-scoped index (see
	// rancher-2.15-catalog-sharding-issue.md at the repo root).
	catalogClient, err := s.client.GetClusterCatalogClient(s.cluster.ID)
	require.NoError(s.T(), err)
	latest, err := catalogClient.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
	require.NoError(s.T(), err)

	installOptions := &charts.InstallOptions{
		Cluster: s.cluster,
		Version: latest,
	}
	err = charts.RetryOnWatchError(charts.DefaultWatchRetries, func() error {
		return charts.UpgradeLonghornChart(s.client, installOptions)
	})
	require.NoError(s.T(), err)

	chart, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(s.T(), err)
	require.Equal(s.T(), latest, chart.ChartDetails.Spec.Chart.Metadata.Version)

	err = storage.VerifyChecksumWorkload(s.client, s.cluster.ID, checksumWorkloadName, checksumContent)
	require.NoError(s.T(), err)
}

func TestLonghornUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornUpgradeTestSuite))
}

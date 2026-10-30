//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

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
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type LonghornUpgradeTestSuite struct {
	suite.Suite
	session *session.Session
	client  *rancher.Client
	cluster *clusters.ClusterMeta
}

func (s *LonghornUpgradeTestSuite) SetupSuite() {
	cfg := loadUpgradeInput()
	pipeline := new(PipelineInput)
	config.LoadConfig(PipelineConfigKey, pipeline)

	if cfg.TargetVersion == "" {
		// The dedicated pipeline always writes both keys, so an empty target
		// there is a pipeline bug and must fail, not skip.
		require.False(s.T(), pipeline.Enabled, "rancher-upgrade pipeline config is missing %s.targetVersion", UpgradeInputConfigKey)
		// Generic PIT legs never upgrade the server between phases; without
		// an upgrade target the phases have nothing to assert, so skip.
		s.T().Skipf("%s.targetVersion is not set: this suite runs only in the rancher-upgrade pipeline, which upgrades the server between phases", UpgradeInputConfigKey)
	}

	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(s.T(), err)
	s.client = client

	s.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
}

// TearDownSuite deliberately skips session.Cleanup(): the pre-upgrade resources must
// survive into the post-upgrade and chart-upgrade phases, which run as separate go
// test processes. The pipeline environment teardown is the cleanup.
func (s *LonghornUpgradeTestSuite) TearDownSuite() {
}

func (s *LonghornUpgradeTestSuite) TestLonghornPreUpgrade() {
	logrus.Infof("pre-upgrade phase: creating project on cluster %s", s.cluster.ID)
	project, _, err := projects.CreateProjectAndNamespace(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	// Resolve from the cluster-scoped catalog: the management view can offer versions
	// the cluster-scoped shard does not serve yet (rancher-2.15-catalog-sharding-issue.md),
	// and the chart action validates against the cluster-scoped index.
	catalogClient, err := s.client.GetClusterCatalogClient(s.cluster.ID)
	require.NoError(s.T(), err)
	version, err := catalogClient.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
	require.NoError(s.T(), err)
	logrus.Infof("pre-upgrade phase: installing longhorn %s from the %s repo", version, catalog.RancherChartRepo)

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
	logrus.Infof("pre-upgrade phase: longhorn %s installed; seeding checksum workload %s", version, checksumWorkloadName)

	err = storage.CreateChecksumWorkload(s.client, s.cluster.ID, charts.LonghornStorageClass, checksumWorkloadName, checksumContent)
	require.NoError(s.T(), err)
	logrus.Infof("pre-upgrade phase: checksum workload %s ready; pre-upgrade state seeded", checksumWorkloadName)
}

func (s *LonghornUpgradeTestSuite) TestLonghornPostUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("post-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: server version %s observed; checking longhorn survival", cfg.TargetVersion)

	chart, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(s.T(), err)
	require.True(s.T(), chart.IsAlreadyInstalled, "longhorn chart must still be installed after the Rancher server upgrade")
	logrus.Infof("post-upgrade phase: longhorn chart still installed; verifying workload data")

	err = storage.VerifyChecksumWorkload(s.client, s.cluster.ID, checksumWorkloadName, checksumContent)
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: checksum verified on %s", checksumWorkloadName)
}

func (s *LonghornUpgradeTestSuite) TestLonghornChartUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("chart-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)

	// Resolve the latest version from the cluster-scoped catalog: the management
	// view can report versions absent from the cluster-scoped index (see
	// rancher-2.15-catalog-sharding-issue.md at the repo root).
	catalogClient, err := s.client.GetClusterCatalogClient(s.cluster.ID)
	require.NoError(s.T(), err)
	latest, err := catalogClient.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
	require.NoError(s.T(), err)

	// Guard against a stale cluster-scoped catalog: until the cluster agent re-syncs,
	// the follower can still serve the previous index, whose latest equals the
	// installed version; that upgrade would pass vacuously.
	installed, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(s.T(), err)
	require.NotEqual(s.T(),
		installed.ChartDetails.Spec.Chart.Metadata.Version, latest,
		"cluster-scoped catalog still serves the pre-upgrade index: installed longhorn %s equals latest %s; restart the cluster agent and re-run this phase",
		installed.ChartDetails.Spec.Chart.Metadata.Version, latest)
	logrus.Infof("chart-upgrade phase: upgrading longhorn %s to %s from the upgraded catalog",
		installed.ChartDetails.Spec.Chart.Metadata.Version, latest)

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
	logrus.Infof("chart-upgrade phase: longhorn at %s; verifying workload data", latest)

	err = storage.VerifyChecksumWorkload(s.client, s.cluster.ID, checksumWorkloadName, checksumContent)
	require.NoError(s.T(), err)
	logrus.Infof("chart-upgrade phase: checksum verified on %s", checksumWorkloadName)
}

func TestLonghornUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornUpgradeTestSuite))
}

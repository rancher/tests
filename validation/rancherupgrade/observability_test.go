//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"context"
	"testing"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	k8sErrors "k8s.io/apimachinery/pkg/api/errors"
	meta "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/rancher/shepherd/clients/rancher"
	extencharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	interoperableCharts "github.com/rancher/tests/interoperability/charts"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type ObservabilityUpgradeTestSuite struct {
	suite.Suite
	session *session.Session
	client  *rancher.Client
	// serverCluster is the Rancher local cluster hosting the suse-observability
	// server chart; agentCluster is the shared downstream cluster selected by
	// rancher.clusterName (rancher/tests#946 user story 18) hosting the agent.
	serverCluster *clusters.ClusterMeta
	agentCluster  *clusters.ClusterMeta
}

func (s *ObservabilityUpgradeTestSuite) SetupSuite() {
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

	s.serverCluster, err = clusters.NewClusterMeta(client, localClusterName)
	require.NoError(s.T(), err)

	s.agentCluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
}

// TearDownSuite deliberately does not call session.Cleanup(): resources created in
// the pre-upgrade phase (suse-observability server chart and stackstate agent chart
// with their namespaces) must survive into the post-upgrade and chart-upgrade
// phases, which run as separate go test processes. The pipeline environment
// teardown is the cleanup for these resources.
func (s *ObservabilityUpgradeTestSuite) TearDownSuite() {
}

func (s *ObservabilityUpgradeTestSuite) TestObservabilityPreUpgrade() {
	stackstateConfigs := loadStackStateConfig()
	require.NotEmpty(s.T(), stackstateConfigs.Url, "stackstateConfigs.url must be set to install SUSE Observability")
	require.NotEmpty(s.T(), stackstateConfigs.ClusterApiKey, "stackstateConfigs.clusterApiKey must be set to install SUSE Observability")

	// Server side, on the Rancher local cluster. Resolve the version from the
	// cluster-scoped catalog: the management view can offer versions the
	// cluster-scoped shard does not serve yet (rancher-2.15-catalog-sharding-issue.md),
	// and the chart action validates against the cluster-scoped index.
	serverCatalogClient, err := s.client.GetClusterCatalogClient(s.serverCluster.ID)
	require.NoError(s.T(), err)

	_, err = serverCatalogClient.ClusterRepos().Get(context.TODO(), interoperableCharts.StackStateServerChartRepo, meta.GetOptions{})
	if k8sErrors.IsNotFound(err) {
		logrus.Infof("pre-upgrade phase: creating %s ClusterRepo at %s", interoperableCharts.StackStateServerChartRepo, observabilityServerChartURL)
		err = interoperableCharts.CreateClusterRepo(s.client, serverCatalogClient, interoperableCharts.StackStateServerChartRepo, observabilityServerChartURL)
	}
	require.NoError(s.T(), err)

	// The ClusterRepo above may have been created seconds ago; its chart index
	// is fetched and indexed asynchronously, so poll for index readiness first.
	// Then seed the previous stable version, not the latest: the prime repo's
	// index does not change across the Rancher server upgrade, so a
	// latest-seeded install leaves the chart-upgrade phase without a delta.
	_, err = latestChartVersionAfterRepoCreate(serverCatalogClient, interoperableCharts.StackStateServerChartRepo, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)
	seedServerVersion, err := previousStableChartVersion(serverCatalogClient, interoperableCharts.StackStateServerChartRepo, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)

	// Pre-warm the version's chart template: its first fetch downloads the
	// chart tarball server-side, and an install action issued before the
	// template is cached can outlive the request deadline.
	require.NoError(s.T(), interoperableCharts.WaitForChartTemplate(serverCatalogClient, interoperableCharts.StackStateServerChartRepo, interoperableCharts.StackStateServerChartRepo, seedServerVersion))

	// The install action creates and project-annotates the namespace itself;
	// pre-creating it poisons the action's thirty-second watch for the
	// InitialRolesPopulated event on an already-quiet namespace.

	serverSystemProjectID, err := systemProjectID(s.client, s.serverCluster.ID)
	require.NoError(s.T(), err)

	serverValues, err := stackstateServerValues(stackstateConfigs)
	require.NoError(s.T(), err)

	// The chart's PVCs request the cluster default storage class; the
	// pipeline's RKE2 clusters ship none, which leaves every PVC Pending and
	// helm --wait burning its full deadline.
	require.NoError(s.T(), ensureDefaultStorageClass(s.client, s.serverCluster.ID))

	// Re-entrant install: a partial install from an earlier attempt leaves the
	// helm release name taken ("cannot re-use a name that is still in use").
	// A deployed app already satisfies the phase; a stale partial one is
	// uninstalled first.
	serverInstalled := false
	if existing, statusErr := extencharts.GetChartStatus(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo); statusErr == nil && existing != nil && existing.ChartDetails != nil {
		if existing.ChartDetails.Status.Summary.State == string(catalogv1.StatusDeployed) {
			logrus.Infof("pre-upgrade phase: suse-observability server already deployed at %s; skipping install", existing.ChartDetails.Spec.Chart.Metadata.Version)
			serverInstalled = true
		} else {
			logrus.Infof("pre-upgrade phase: stale suse-observability app in state %s; uninstalling before install", existing.ChartDetails.Status.Summary.State)
			require.NoError(s.T(), interoperableCharts.UninstallStackStateApp(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo))
		}
	}
	if !serverInstalled {
		logrus.Infof("pre-upgrade phase: installing suse-observability server %s in the System project on the local cluster", seedServerVersion)
		err = interoperableCharts.InstallStackStateServerChart(s.client, &charts.InstallOptions{
			Cluster:   s.serverCluster,
			Version:   seedServerVersion,
			ProjectID: serverSystemProjectID,
		}, serverSystemProjectID, serverValues)
		if err != nil {
			// The install action answers bare internal errors whose cause only
			// the Rancher server knows; capture its logs, the namespace state and
			// the helm operation's status before failing.
			logInstallDiagnostics(s.client, serverCatalogClient, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo)
		}
		require.NoError(s.T(), err)
	}

	err = waitForChartWorkloads(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace)
	require.NoError(s.T(), err)

	serverPodClusterName, err := podStatusClusterName(s.client, localClusterName)
	require.NoError(s.T(), err)
	require.Empty(s.T(), pods.StatusPods(s.client, serverPodClusterName), "suse-observability server pods must be healthy after install")
	logrus.Infof("pre-upgrade phase: suse-observability server %s healthy; installing the agent", seedServerVersion)

	// Agent side, on the shared downstream cluster selected by rancher.clusterName.
	agentCatalogClient, err := s.client.GetClusterCatalogClient(s.agentCluster.ID)
	require.NoError(s.T(), err)

	seedAgentVersion, err := previousStableChartVersion(agentCatalogClient, interoperableCharts.StackstateK8sAgent, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)

	// As above on the agent side: the install action owns namespace creation.

	agentSystemProjectID, err := systemProjectID(s.client, s.agentCluster.ID)
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: installing stackstate agent %s on cluster %s", seedAgentVersion, s.agentCluster.ID)
	err = interoperableCharts.InstallStackstateAgentChart(s.client, &charts.InstallOptions{
		Cluster:   s.agentCluster,
		Version:   seedAgentVersion,
		ProjectID: agentSystemProjectID,
	}, stackstateConfigs, agentSystemProjectID)
	if err != nil {
		// The install action answers bare internal errors whose cause only
		// the Rancher server knows; capture the same evidence the server
		// install path collects.
		logInstallDiagnostics(s.client, agentCatalogClient, s.agentCluster.ID, interoperableCharts.StackstateNamespace, interoperableCharts.StackstateK8sAgent)
	}
	require.NoError(s.T(), err)

	// Agent reporting is verified the way validation/observability does it: the
	// agent's deployments and daemonsets reach their expected replicas and its
	// pods report healthy against the server.
	err = waitForChartWorkloads(s.client, s.agentCluster.ID, interoperableCharts.StackstateNamespace)
	require.NoError(s.T(), err)

	agentPodClusterName, err := podStatusClusterName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
	require.Empty(s.T(), pods.StatusPods(s.client, agentPodClusterName), "stackstate agent pods must be healthy and reporting after install")
	logrus.Infof("pre-upgrade phase: stackstate agent %s reporting to %s; pre-upgrade state seeded", seedAgentVersion, stackstateConfigs.Url)
}

func (s *ObservabilityUpgradeTestSuite) TestObservabilityPostUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("post-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: server version %s observed; checking suse-observability server health", cfg.TargetVersion)

	serverChart, err := extencharts.GetChartStatus(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)
	require.True(s.T(), serverChart.IsAlreadyInstalled, "suse-observability server chart must still be installed after the Rancher server upgrade")

	err = waitForChartWorkloads(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace)
	require.NoError(s.T(), err)

	serverPodClusterName, err := podStatusClusterName(s.client, localClusterName)
	require.NoError(s.T(), err)
	require.Empty(s.T(), pods.StatusPods(s.client, serverPodClusterName), "suse-observability server pods must be healthy after the Rancher server upgrade")
	logrus.Infof("post-upgrade phase: suse-observability server healthy; checking agent reporting continuity")

	agentChart, err := extencharts.GetChartStatus(s.client, s.agentCluster.ID, interoperableCharts.StackstateNamespace, interoperableCharts.StackstateK8sAgent)
	require.NoError(s.T(), err)
	require.True(s.T(), agentChart.IsAlreadyInstalled, "stackstate agent chart must still be installed after the Rancher server upgrade")

	err = waitForChartWorkloads(s.client, s.agentCluster.ID, interoperableCharts.StackstateNamespace)
	require.NoError(s.T(), err)

	agentPodClusterName, err := podStatusClusterName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
	require.Empty(s.T(), pods.StatusPods(s.client, agentPodClusterName), "stackstate agent pods must still be healthy and reporting after the Rancher server upgrade")
	logrus.Infof("post-upgrade phase: monitoring continuity confirmed; stackstate agent still reporting to the server")
}

func (s *ObservabilityUpgradeTestSuite) TestObservabilityChartUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("chart-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)

	stackstateConfigs := loadStackStateConfig()
	require.NotEmpty(s.T(), stackstateConfigs.Url, "stackstateConfigs.url must be set to upgrade SUSE Observability")
	require.NotEmpty(s.T(), stackstateConfigs.ClusterApiKey, "stackstateConfigs.clusterApiKey must be set to upgrade SUSE Observability")

	// Resolve the latest versions from the cluster-scoped catalogs, not the
	// management view: the management view can report versions absent from the
	// cluster-scoped index (rancher-2.15-catalog-sharding-issue.md). The server
	// resolves via the prime server-charts ClusterRepo on the local cluster, the
	// agent via rancher-partner-charts on the downstream cluster.
	serverCatalogClient, err := s.client.GetClusterCatalogClient(s.serverCluster.ID)
	require.NoError(s.T(), err)
	latestServerVersion, err := serverCatalogClient.GetLatestChartVersion(interoperableCharts.StackStateServerChartRepo, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)

	agentCatalogClient, err := s.client.GetClusterCatalogClient(s.agentCluster.ID)
	require.NoError(s.T(), err)
	latestAgentVersion, err := agentCatalogClient.GetLatestChartVersion(interoperableCharts.StackstateK8sAgent, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)

	// Pre-warm the newly offered versions' templates: a version's first
	// template fetch downloads the chart tarball server-side, and an upgrade
	// action issued before the template is cached can outlive the request
	// deadline.
	require.NoError(s.T(), interoperableCharts.WaitForChartTemplate(serverCatalogClient, interoperableCharts.StackStateServerChartRepo, interoperableCharts.StackStateServerChartRepo, latestServerVersion))
	require.NoError(s.T(), interoperableCharts.WaitForChartTemplate(agentCatalogClient, interoperableCharts.StackStateServerChartRepo, interoperableCharts.StackstateK8sAgent, latestAgentVersion))

	installedServer, err := extencharts.GetChartStatus(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)

	installedAgent, err := extencharts.GetChartStatus(s.client, s.agentCluster.ID, interoperableCharts.StackstateNamespace, interoperableCharts.StackstateK8sAgent)
	require.NoError(s.T(), err)

	// Guard against a stale cluster-scoped catalog: until the cluster agent
	// re-syncs after the server upgrade, the follower can still serve the
	// previous Rancher line's index, whose latest equals the installed version.
	// Upgrading to it would pass vacuously (a same-version upgrade asserted
	// equal to itself), so fail with the runbook remedy instead; see README Notes.
	require.NotEqual(s.T(),
		installedServer.ChartDetails.Spec.Chart.Metadata.Version, latestServerVersion,
		"local cluster-scoped catalog still serves the pre-upgrade index: installed suse-observability %s equals latest %s; restart the local cluster agent and re-run this phase",
		installedServer.ChartDetails.Spec.Chart.Metadata.Version, latestServerVersion)
	require.NotEqual(s.T(),
		installedAgent.ChartDetails.Spec.Chart.Metadata.Version, latestAgentVersion,
		"downstream cluster-scoped catalog still serves the pre-upgrade index: installed stackstate agent %s equals latest %s; restart the downstream cluster agent and re-run this phase",
		installedAgent.ChartDetails.Spec.Chart.Metadata.Version, latestAgentVersion)

	// Re-apply the install-time server values rather than a blind version bump:
	// upgrading on chart defaults would drop the sizing profile and the
	// stackstate service credentials (rancher/tests#946 Further Notes on
	// per-product values migration).
	serverValues, err := stackstateServerValues(stackstateConfigs)
	require.NoError(s.T(), err)

	serverSystemProjectID, err := systemProjectID(s.client, s.serverCluster.ID)
	require.NoError(s.T(), err)
	agentSystemProjectID, err := systemProjectID(s.client, s.agentCluster.ID)
	require.NoError(s.T(), err)

	// Upgrade the server first so the agent upgrades against a live, current server.
	logrus.Infof("chart-upgrade phase: upgrading suse-observability server %s to %s",
		installedServer.ChartDetails.Spec.Chart.Metadata.Version, latestServerVersion)
	serverInstallOptions := &charts.InstallOptions{
		Cluster:   s.serverCluster,
		Version:   latestServerVersion,
		ProjectID: serverSystemProjectID,
	}
	err = charts.RetryOnWatchError(charts.DefaultWatchRetries, func() error {
		return interoperableCharts.UpgradeStackStateServerChart(s.client, serverInstallOptions, serverValues)
	})
	if err != nil {
		// Upgrade failures surface the same bare internal errors as installs;
		// capture the helm operation, app state, workload state and rancher
		// tails before failing (build 49's all-module run timed out here
		// with no in-band evidence).
		logInstallDiagnostics(s.client, serverCatalogClient, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo)
	}
	require.NoError(s.T(), err)

	upgradedServer, err := extencharts.GetChartStatus(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace, interoperableCharts.StackStateServerChartRepo)
	require.NoError(s.T(), err)
	require.Equal(s.T(), latestServerVersion, upgradedServer.ChartDetails.Spec.Chart.Metadata.Version)

	err = waitForChartWorkloads(s.client, s.serverCluster.ID, interoperableCharts.StackStateServerNamespace)
	require.NoError(s.T(), err)
	logrus.Infof("chart-upgrade phase: suse-observability server at %s and healthy; upgrading the agent", latestServerVersion)

	logrus.Infof("chart-upgrade phase: upgrading stackstate agent %s to %s",
		installedAgent.ChartDetails.Spec.Chart.Metadata.Version, latestAgentVersion)
	agentInstallOptions := &charts.InstallOptions{
		Cluster:   s.agentCluster,
		Version:   latestAgentVersion,
		ProjectID: agentSystemProjectID,
	}
	err = charts.RetryOnWatchError(charts.DefaultWatchRetries, func() error {
		return interoperableCharts.UpgradeStackstateAgentChart(s.client, agentInstallOptions, stackstateConfigs, agentSystemProjectID)
	})
	if err != nil {
		// Same evidence capture as the server upgrade path above.
		logInstallDiagnostics(s.client, agentCatalogClient, s.agentCluster.ID, interoperableCharts.StackstateNamespace, interoperableCharts.StackstateK8sAgent)
	}
	require.NoError(s.T(), err)

	upgradedAgent, err := extencharts.GetChartStatus(s.client, s.agentCluster.ID, interoperableCharts.StackstateNamespace, interoperableCharts.StackstateK8sAgent)
	require.NoError(s.T(), err)
	require.Equal(s.T(), latestAgentVersion, upgradedAgent.ChartDetails.Spec.Chart.Metadata.Version)

	err = waitForChartWorkloads(s.client, s.agentCluster.ID, interoperableCharts.StackstateNamespace)
	require.NoError(s.T(), err)

	agentPodClusterName, err := podStatusClusterName(s.client, s.client.RancherConfig.ClusterName)
	require.NoError(s.T(), err)
	require.Empty(s.T(), pods.StatusPods(s.client, agentPodClusterName), "stackstate agent pods must be healthy and reporting after the chart upgrade")
	logrus.Infof("chart-upgrade phase: stackstate agent at %s and reporting; observability chart upgrade complete", latestAgentVersion)
}

func TestObservabilityUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(ObservabilityUpgradeTestSuite))
}

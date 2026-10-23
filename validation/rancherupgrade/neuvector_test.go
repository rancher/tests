//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"fmt"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	actionsClusters "github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/neuvector"
	"github.com/rancher/tests/actions/projects"
	validationCharts "github.com/rancher/tests/validation/charts"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type NeuVectorUpgradeTestSuite struct {
	suite.Suite
	session *session.Session
	client  *rancher.Client
	cluster *clusters.ClusterMeta
}

// nvAdmissionDenyNamespace is the deterministic namespace protected by the
// NeuVector admission control deny rule: workload creates in it must be
// rejected by the admission webhook before and after the Rancher server
// upgrade and again after the NeuVector chart upgrade. The deny namespace and
// the NvAdmissionControlSecurityRule are created in the pre-upgrade phase and
// must survive every later phase, which run as separate go test processes.
const nvAdmissionDenyNamespace = "rancher-upgrade-neuvector-deny"

// nvSystemProject returns the System project of the target cluster, which the
// NeuVector chart is installed into, the same placement the hardened NeuVector
// suite uses.
func nvSystemProject(client *rancher.Client, clusterID string) (*management.Project, error) {
	project, err := projects.GetProjectByName(client, clusterID, charts.SystemProject)
	if err != nil {
		return nil, err
	}

	if project.Name != charts.SystemProject {
		return nil, fmt.Errorf("expected project %q on cluster %s, got %q", charts.SystemProject, clusterID, project.Name)
	}

	return project, nil
}

// nvSystemDefaultRegistry returns the system-default-registry setting value. It
// is empty outside airgap environments and must be passed to the NeuVector chart
// install so the images come from the mirrored registry in airgap environments.
func nvSystemDefaultRegistry(client *rancher.Client) (string, error) {
	registrySetting, err := client.Management.Setting.ByID("system-default-registry")
	if err != nil {
		return "", err
	}

	return registrySetting.Value, nil
}

func (s *NeuVectorUpgradeTestSuite) SetupSuite() {
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

// TearDownSuite deliberately does not call session.Cleanup(): resources created in
// the pre-upgrade phase (NeuVector chart, admission control deny rule and its deny
// namespace) must survive into the post-upgrade and chart-upgrade phases, which
// run as separate go test processes. The pipeline environment teardown is the
// cleanup for these resources.
func (s *NeuVectorUpgradeTestSuite) TearDownSuite() {
}

func (s *NeuVectorUpgradeTestSuite) TestNeuVectorPreUpgrade() {
	logrus.Infof("pre-upgrade phase: resolving the System project on cluster %s", s.cluster.ID)
	project, err := nvSystemProject(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	// Resolve the version from the cluster-scoped catalog and install that: the
	// management view can offer versions the cluster-scoped shard does not serve
	// yet (rancher-2.15-catalog-sharding-issue.md), and the chart action validates
	// against the cluster-scoped index. The pre-phase still yields the 2.14-era
	// version by construction, since it runs against the pre-upgrade Rancher.
	catalogClient, err := s.client.GetClusterCatalogClient(s.cluster.ID)
	require.NoError(s.T(), err)
	version, err := catalogClient.GetLatestChartVersion(charts.NeuVectorChartName, catalog.RancherChartRepo)
	require.NoError(s.T(), err)

	defaultRegistry, err := nvSystemDefaultRegistry(s.client)
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: installing neuvector %s from the %s repo", version, catalog.RancherChartRepo)
	err = charts.InstallNeuVectorChart(s.client, charts.PayloadOpts{
		Namespace: charts.NeuVectorNamespace,
		Host:      s.client.RancherConfig.Host,
		InstallOptions: charts.InstallOptions{
			Cluster:   s.cluster,
			Version:   version,
			ProjectID: project.ID,
		},
		DefaultRegistry: defaultRegistry,
		K3s:             s.cluster.Provider == clusters.KubernetesProviderK3S,
	})
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: neuvector %s installed; waiting for its workloads", version)
	err = charts.RetryOnWatchError(charts.DefaultWatchRetries, func() error {
		return shepherdCharts.WaitChartInstall(catalogClient, charts.NeuVectorNamespace, charts.NeuVectorChartName)
	})
	require.NoError(s.T(), err)

	err = shepherdCharts.WatchAndWaitDeployments(s.client, s.cluster.ID, charts.NeuVectorNamespace, metav1.ListOptions{})
	require.NoError(s.T(), err)
	err = shepherdCharts.WatchAndWaitDaemonSets(s.client, s.cluster.ID, charts.NeuVectorNamespace, metav1.ListOptions{})
	require.NoError(s.T(), err)

	logrus.Infof("pre-upgrade phase: enabling the admission control deny rule for namespace %s", nvAdmissionDenyNamespace)
	err = neuvector.EnableAdmissionControlDenyNamespace(s.client, s.cluster.ID, nvAdmissionDenyNamespace)
	require.NoError(s.T(), err)

	err = neuvector.VerifyAdmissionDenial(s.client, s.cluster.ID, nvAdmissionDenyNamespace)
	require.NoError(s.T(), err)
	logrus.Infof("pre-upgrade phase: admission denial proven in %s; pre-upgrade state seeded", nvAdmissionDenyNamespace)
}

func (s *NeuVectorUpgradeTestSuite) TestNeuVectorPostUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("post-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: server version %s observed; checking neuvector survival", cfg.TargetVersion)

	chart, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.NeuVectorNamespace, charts.NeuVectorChartName)
	require.NoError(s.T(), err)
	require.True(s.T(), chart.IsAlreadyInstalled, "neuvector chart must still be installed after the Rancher server upgrade")
	logrus.Infof("post-upgrade phase: neuvector chart still installed; verifying services and manager UI")

	err = validationCharts.VerifyNeuVectorServicesActive(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	respUI, err := validationCharts.VerifyNeuVectorWebUIAccessible(s.client, s.cluster.ID)
	require.NoError(s.T(), err, "neuvector manager web UI should be reachable via the Rancher service proxy")
	require.NotEmpty(s.T(), respUI, "expected a non-empty response from the neuvector manager web UI")
	logrus.Infof("post-upgrade phase: services active and manager UI reachable; verifying admission enforcement")

	err = neuvector.VerifyAdmissionDenial(s.client, s.cluster.ID, nvAdmissionDenyNamespace)
	require.NoError(s.T(), err)
	logrus.Infof("post-upgrade phase: admission webhook still denies creates in %s", nvAdmissionDenyNamespace)
}

func (s *NeuVectorUpgradeTestSuite) TestNeuVectorChartUpgrade() {
	cfg := loadUpgradeInput()
	logrus.Infof("chart-upgrade phase: waiting for rancher server version %s", cfg.TargetVersion)

	err := actionsClusters.WaitRancherVersion(s.client, cfg.TargetVersion, defaults.TenMinuteTimeout)
	require.NoError(s.T(), err)

	// Resolve the latest version from the cluster-scoped catalog: the management
	// view can report versions absent from the cluster-scoped index (see
	// rancher-2.15-catalog-sharding-issue.md at the repo root).
	catalogClient, err := s.client.GetClusterCatalogClient(s.cluster.ID)
	require.NoError(s.T(), err)
	latest, err := catalogClient.GetLatestChartVersion(charts.NeuVectorChartName, catalog.RancherChartRepo)
	require.NoError(s.T(), err)

	// Guard against a stale cluster-scoped catalog: until the cluster agent
	// re-syncs after the server upgrade, the follower can still serve the
	// previous Rancher line's index, whose latest equals the installed version.
	// Upgrading to it would pass vacuously (a same-version upgrade asserted
	// equal to itself), so fail with the runbook remedy instead; see README Notes.
	installed, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.NeuVectorNamespace, charts.NeuVectorChartName)
	require.NoError(s.T(), err)
	require.NotEqual(s.T(),
		installed.ChartDetails.Spec.Chart.Metadata.Version, latest,
		"cluster-scoped catalog still serves the pre-upgrade index: installed neuvector %s equals latest %s; restart the cluster agent and re-run this phase",
		installed.ChartDetails.Spec.Chart.Metadata.Version, latest)
	logrus.Infof("chart-upgrade phase: upgrading neuvector %s to %s from the upgraded catalog",
		installed.ChartDetails.Spec.Chart.Metadata.Version, latest)

	installOptions := &charts.InstallOptions{
		Cluster: s.cluster,
		Version: latest,
	}
	err = charts.RetryOnWatchError(charts.DefaultWatchRetries, func() error {
		return charts.UpgradeNeuVectorChart(s.client, installOptions)
	})
	require.NoError(s.T(), err)

	chart, err := shepherdCharts.GetChartStatus(s.client, s.cluster.ID, charts.NeuVectorNamespace, charts.NeuVectorChartName)
	require.NoError(s.T(), err)
	require.Equal(s.T(), latest, chart.ChartDetails.Spec.Chart.Metadata.Version)
	logrus.Infof("chart-upgrade phase: neuvector at %s; verifying health and enforcement", latest)

	err = validationCharts.VerifyNeuVectorServicesActive(s.client, s.cluster.ID)
	require.NoError(s.T(), err)

	err = neuvector.VerifyAdmissionDenial(s.client, s.cluster.ID, nvAdmissionDenyNamespace)
	require.NoError(s.T(), err)
	logrus.Infof("chart-upgrade phase: admission webhook still denies creates in %s", nvAdmissionDenyNamespace)
}

func TestNeuVectorUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(NeuVectorUpgradeTestSuite))
}

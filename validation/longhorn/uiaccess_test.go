//go:build validation || pit.daily

package longhorn

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/interoperability/longhorn"
	longhornapi "github.com/rancher/tests/interoperability/longhorn/api"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

// LonghornUIAccessTestSuite is a test suite for validating Longhorn UI and API access on downstream clusters
type LonghornUIAccessTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	longhornTestConfig longhorn.TestConfig
	cluster            *clusters.ClusterMeta
	project            *management.Project
}

func (l *LonghornUIAccessTestSuite) TearDownSuite() {
	l.session.Cleanup()
}

func (l *LonghornUIAccessTestSuite) SetupSuite() {
	l.session = session.NewSession()

	client, err := rancher.NewClient("", l.session)
	require.NoError(l.T(), err)
	l.client = client

	l.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(l.T(), err)

	l.longhornTestConfig = *longhorn.GetLonghornTestConfig()

	projectConfig := &management.Project{
		ClusterID: l.cluster.ID,
		Name:      l.longhornTestConfig.LonghornTestProject,
	}

	l.project, err = client.Management.Project.Create(projectConfig)
	require.NoError(l.T(), err)

	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	if !chart.IsAlreadyInstalled {
		// Get latest versions of longhorn
		latestLonghornVersion, err := l.client.Catalog.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
		require.NoError(l.T(), err)

		payloadOpts := charts.PayloadOpts{
			Namespace: charts.LonghornNamespace,
			Host:      l.client.RancherConfig.Host,
			InstallOptions: charts.InstallOptions{
				Cluster:   l.cluster,
				Version:   latestLonghornVersion,
				ProjectID: l.project.ID,
			},
		}

		l.T().Logf("Installing Longhorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, payloadOpts.Version, l.project.Name, payloadOpts.Namespace)
		err = charts.InstallLonghornChart(l.client, payloadOpts, nil)
		require.NoError(l.T(), err)
	}
}

func (l *LonghornUIAccessTestSuite) TestLonghornUIAccess() {
	l.T().Log("Verifying all Longhorn pods are in active state")
	err := validateLonghornPods(l.T(), l.client, l.cluster.ID)
	require.NoError(l.T(), err)

	l.T().Log("Verifying Longhorn service is accessible")
	serviceURL, err := validateLonghornService(l.T(), l.client, l.cluster.ID)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), serviceURL)

	l.T().Logf("Longhorn service URL: %s", serviceURL)
	l.T().Log("Verifying Longhorn API accessibility")
	apiClient, err := longhornapi.NewLonghornClient(l.client, l.cluster.ID, serviceURL)
	require.NoError(l.T(), err)

	l.T().Log("Validating Longhorn nodes show valid state")
	err = longhornapi.ValidateNodes(apiClient)
	require.NoError(l.T(), err)

	l.T().Log("Validating Longhorn settings are properly configured")
	err = longhornapi.ValidateSettings(apiClient)
	require.NoError(l.T(), err)

	l.T().Log("Creating Longhorn volume through Longhorn API")
	volumeName, err := longhornapi.CreateVolume(l.T(), apiClient)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), volumeName)

	// Register cleanup function for the volume
	l.session.RegisterCleanupFunc(func() error {
		l.T().Logf("Cleaning up test volume: %s", volumeName)
		return longhornapi.DeleteVolume(apiClient, volumeName)
	})

	l.T().Logf("Validating volume %s is active through Longhorn API", volumeName)
	err = longhornapi.ValidateVolumeActive(l.T(), apiClient, volumeName)
	require.NoError(l.T(), err)

	l.T().Logf("Validating volume %s is ready through Rancher API", volumeName)
	err = longhornapi.ValidateVolumeInRancherAPI(l.T(), apiClient, volumeName)
	require.NoError(l.T(), err)

	l.T().Log("Verifying Longhorn storage class is accessible through Rancher API")
	err = validateLonghornStorageClassInRancher(l.T(), l.client, l.cluster.ID, l.longhornTestConfig.LonghornTestStorageClass)
	require.NoError(l.T(), err)
}

func (l *LonghornUIAccessTestSuite) TestLonghornUIDynamic() {
	l.T().Log("Verifying all Longhorn pods are in active state")
	err := validateLonghornPods(l.T(), l.client, l.cluster.ID)
	require.NoError(l.T(), err)

	l.T().Log("Verifying Longhorn service is accessible")
	serviceURL, err := validateLonghornService(l.T(), l.client, l.cluster.ID)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), serviceURL)

	l.T().Logf("Longhorn service URL: %s", serviceURL)
	l.T().Log("Verifying Longhorn API accessibility with dynamic configuration")
	apiClient, err := longhornapi.NewLonghornClient(l.client, l.cluster.ID, serviceURL)
	require.NoError(l.T(), err)

	l.T().Log("Validating Longhorn configuration based on user-provided settings")
	err = longhornapi.ValidateDynamicConfiguration(l.T(), apiClient, l.longhornTestConfig)
	require.NoError(l.T(), err)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornUIAccessTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornUIAccessTestSuite))
}

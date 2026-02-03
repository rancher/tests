//go:build validation || pit.daily || pit.harvester.daily

package longhorn

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	shepherdPods "github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/storage"
	"github.com/rancher/tests/interoperability/longhorn"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	longhornStorageClass       = "longhorn"
	longhornStaticStorageClass = "longhorn-static"
	createDefaultDiskNodeLabel = "node.longhorn.io/create-default-disk=true"
)

type LonghornChartTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	longhornTestConfig longhorn.TestConfig
	cluster            *clusters.ClusterMeta
	project            *management.Project
	payloadOpts        charts.PayloadOpts
}

func (l *LonghornChartTestSuite) TearDownSuite() {
	l.session.Cleanup()
}

func (l *LonghornChartTestSuite) SetupSuite() {
	l.session = session.NewSession()

	client, err := rancher.NewClient("", l.session)
	require.NoError(l.T(), err)
	l.client = client

	l.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(l.T(), err)

	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	l.longhornTestConfig = *longhorn.GetLonghornTestConfig()

	if chart.IsAlreadyInstalled {
		l.T().Skip("Skipping Longhorn chart tests as Longhorn is already installed on the provided cluster.")
	}

	projectConfig := &management.Project{
		ClusterID: l.cluster.ID,
		Name:      l.longhornTestConfig.LonghornTestProject,
	}

	l.project, err = client.Management.Project.Create(projectConfig)
	require.NoError(l.T(), err)

	// Get latest versions of longhorn
	latestLonghornVersion, err := l.client.Catalog.GetLatestChartVersion(charts.LonghornChartName, catalog.RancherChartRepo)
	require.NoError(l.T(), err)

	l.payloadOpts = charts.PayloadOpts{
		Namespace: charts.LonghornNamespace,
		Host:      l.client.RancherConfig.Host,
		InstallOptions: charts.InstallOptions{
			Cluster:   l.cluster,
			Version:   latestLonghornVersion,
			ProjectID: l.project.ID,
		},
	}
}

func (l *LonghornChartTestSuite) TestChartInstall() {
	l.T().Logf("Installing Longhorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
	err := charts.InstallLonghornChart(l.client, l.payloadOpts, nil)
	require.NoError(l.T(), err)

	l.T().Logf("Create nginx deployment with %s PVC on default namespace", longhornStorageClass)
	nginxResponse := storage.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, longhornStorageClass)

	err = shepherdCharts.WatchAndWaitDeployments(l.client, l.cluster.ID, namespaces.Default, metav1.ListOptions{})
	require.NoError(l.T(), err)

	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	pods, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespaces.Default).List(nil)
	require.NotEmpty(l.T(), pods)
	require.NoError(l.T(), err)

	var podName string
	for _, pod := range pods.Data {
		if strings.Contains(pod.Name, nginxResponse.ObjectMeta.Name) {
			podName = pod.Name
			break
		}
	}

	storage.CheckMountedVolume(l.T(), l.client, l.cluster.ID, namespaces.Default, podName, storage.MountPath)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornChartInstallTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornChartTestSuite))
}

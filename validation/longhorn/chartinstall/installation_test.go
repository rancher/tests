//go:build validation || pit.daily

package longhorn

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/kubectl"
	shepherdPods "github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/storage"
	"github.com/rancher/tests/interoperability/longhorn"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appv1 "k8s.io/api/apps/v1"
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

func (l *LonghornChartTestSuite) TestChartInstallStaticCustomConfig() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	// If Longhorn was installed by a previous test on this same session, uninstall it to install it again with custom configuration.
	if chart.IsAlreadyInstalled {
		l.T().Log("Uninstalling Longhorn as it was installed on a previous test.")
		err = charts.UninstallLonghornChart(l.client, charts.LonghornNamespace, l.cluster.ID, l.payloadOpts.Host)
		require.NoError(l.T(), err)
	}

	nodeCollection, err := l.client.Management.Node.List(&types.ListOpts{Filters: map[string]interface{}{
		"clusterId": l.cluster.ID,
	}})
	require.NoError(l.T(), err)

	// Label worker nodes to check effectiveness of createDefaultDiskLabeledNodes setting.
	// Also save the name of one worker node for future use.
	l.T().Log("Label worker nodes with Longhorn's create-default-disk=true")
	var workerName string
	for _, node := range nodeCollection.Data {
		if node.Worker {
			labelNodeCommand := []string{"kubectl", "label", "node", node.Hostname, createDefaultDiskNodeLabel}
			_, err = kubectl.Command(l.client, nil, l.cluster.ID, labelNodeCommand, "")
			require.NoError(l.T(), err)
			if workerName == "" {
				workerName = node.Hostname
			}
		}
	}

	longhornCustomSetting := map[string]any{
		"defaultSettings": map[string]any{
			"createDefaultDiskLabeledNodes":     true,
			"defaultDataPath":                   "/var/lib/longhorn-custom",
			"defaultReplicaCount":               2,
			"storageOverProvisioningPercentage": 150,
		},
	}

	l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
	err = charts.InstallLonghornChart(l.client, l.payloadOpts, longhornCustomSetting)
	require.NoError(l.T(), err)

	expectedSettings := map[string]string{
		"default-data-path":                    "/var/lib/longhorn-custom",
		"default-replica-count":                `{"v1":"2","v2":"2"}`,
		"storage-over-provisioning-percentage": "150",
		"create-default-disk-labeled-nodes":    "true",
	}

	for setting, expectedValue := range expectedSettings {
		getSettingCommand := []string{"kubectl", "-n", charts.LonghornNamespace, "get", "settings.longhorn.io", setting, `-o=jsonpath='{.value}'`}
		settingValue, err := kubectl.Command(l.client, nil, l.cluster.ID, getSettingCommand, "")
		require.NoError(l.T(), err)
		// The output extracted from kubectl has single quotes and a newline on the end.
		require.Equal(l.T(), fmt.Sprintf("'%s'\n", expectedValue), settingValue)
	}

	// Use the "longhorn-static" storage class so we get the expected number of replicas.
	// Using the "longhorn" storage class will always result in 3 volume replicas.
	l.T().Logf("Create nginx deployment with %s PVC on default namespace", longhornStaticStorageClass)
	nginxResponse := storage.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, longhornStaticStorageClass)

	nginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(nginxResponse.Spec, nginxSpec)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), nginxSpec.Template.Spec.Volumes[0])

	// Even though the Longhorn default for number of replicas is 2, Rancher enforces its own default of 3.
	volumeName := nginxSpec.Template.Spec.Volumes[0].Name
	checkReplicasCommand := []string{"kubectl", "-n", charts.LonghornNamespace, "get", "volumes.longhorn.io", volumeName, `-o=jsonpath="{.spec.numberOfReplicas}"`}
	settingValue, err := kubectl.Command(l.client, nil, l.cluster.ID, checkReplicasCommand, "")
	require.NoError(l.T(), err)
	require.Equal(l.T(), "\"2\"\n", settingValue)

	// Check the node's filesystem contains the expected files.
	storage.CheckNodeFilesystem(l.T(), l.client, l.cluster.ID, workerName, "test -d /host/var/lib/longhorn-custom/replicas && test -f /host/var/lib/longhorn-custom/longhorn-disk.cfg", l.project)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornChartInstallTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornChartTestSuite))
}

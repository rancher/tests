//go:build validation

package longhorn

import (
	"fmt"
	"testing"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherd_charts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/cloudprovider"
	namespace_actions "github.com/rancher/tests/actions/namespaces"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	longhornNamespace               = "longhorn-system"
	longhornChartName               = "longhorn"
	longhornProjectName             = "longhorn-test"
	longhornDefaultStorageClassName = "longhorn"
	debugNamespace                  = namegenerator.AppendRandomString("debug-")
	longhornCustomSetting           = map[string]any{
		"defaultSettings": map[string]any{
			"createDefaultDiskLabeledNodes":     true,
			"defaultDataPath":                   "/var/lib/longhorn-custom",
			"defaultReplicaCount":               2,
			"storageOverProvisioningPercentage": 150,
		},
	}
)

type LonghornTestSuite struct {
	suite.Suite
	client      *rancher.Client
	session     *session.Session
	cluster     *clusters.ClusterMeta
	project     *management.Project
	payloadOpts charts.PayloadOpts
}

func (l *LonghornTestSuite) TearDownSuite() {
	l.session.Cleanup()
}

func (l *LonghornTestSuite) SetupSuite() {
	l.session = session.NewSession()

	client, err := rancher.NewClient("", l.session)
	require.NoError(l.T(), err)

	l.client = client

	l.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(l.T(), err)

	projectConfig := &management.Project{
		ClusterID: l.cluster.ID,
		Name:      longhornProjectName,
	}

	l.project, err = client.Management.Project.Create(projectConfig)
	require.NoError(l.T(), err)

	// Get latest versions of longhorn
	latestLonghornVersion, err := l.client.Catalog.GetLatestChartVersion(longhornChartName, catalog.RancherChartRepo)
	require.NoError(l.T(), err)

	l.payloadOpts = charts.PayloadOpts{
		Namespace: longhornNamespace,
		Host:      l.client.RancherConfig.Host,
		InstallOptions: charts.InstallOptions{
			Cluster:   l.cluster,
			Version:   latestLonghornVersion,
			ProjectID: l.project.ID,
		},
	}
}

func (l *LonghornTestSuite) TestChartInstall() {
	chart, err := shepherd_charts.GetChartStatus(l.client, l.cluster.ID, longhornNamespace, longhornChartName)
	require.NoError(l.T(), err)

	if chart.IsAlreadyInstalled {
		l.T().Skip("Skipping installation test because Longhorn is already installed")
	}

	l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
	err = charts.InstallLonghornChart(l.client, l.payloadOpts, nil, true)
	require.NoError(l.T(), err)

	l.T().Logf("Create nginx deployment with Longhorn PVC")
	cloudprovider.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, longhornDefaultStorageClassName)

	err = shepherd_charts.WatchAndWaitDeployments(l.client, l.cluster.ID, namespaces.Default, metav1.ListOptions{})
	require.NoError(l.T(), err)
}

func (l *LonghornTestSuite) TestChartInstallCustomConfig() {
	chart, err := shepherd_charts.GetChartStatus(l.client, l.cluster.ID, longhornNamespace, longhornChartName)
	require.NoError(l.T(), err)

	if chart.IsAlreadyInstalled {
		l.T().Skip("Skipping installation test because Longhorn is already installed")
	}

	nodeCollection, err := l.client.Management.Node.List(&types.ListOpts{Filters: map[string]interface{}{
		"clusterId": l.cluster.ID,
	}})
	require.NoError(l.T(), err)

	// Label worker nodes to check effectiveness of createDefaultDiskLabeledNodes setting.
	// Also save the name of one worker node for future use.
	var workerName string
	for _, node := range nodeCollection.Data {
		if node.Worker {
			labelNodeCommand := []string{"kubectl", "label", "node", node.Hostname, "node.longhorn.io/create-default-disk=true"}
			_, err = kubectl.Command(l.client, nil, l.cluster.ID, labelNodeCommand, "")
			require.NoError(l.T(), err)
			if workerName == "" {
				workerName = node.Hostname
			}
		}
	}

	l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
	err = charts.InstallLonghornChart(l.client, l.payloadOpts, longhornCustomSetting, true)
	require.NoError(l.T(), err)

	settings := map[string]string{
		"default-data-path":                    "/var/lib/longhorn-custom",
		"default-replica-count":                "2",
		"storage-over-provisioning-percentage": "150",
		"create-default-disk-labeled-nodes":    "true",
	}

	for setting, expectedValue := range settings {
		getSettingCommand := []string{"kubectl", "-n", longhornNamespace, "get", "settings.longhorn.io", setting, `-o=jsonpath='{.value}'`}
		settingValue, err := kubectl.Command(l.client, nil, l.cluster.ID, getSettingCommand, "")
		require.NoError(l.T(), err)
		// The output extracted from kubectl has single quotes and a newline on the end.
		require.Equal(l.T(), fmt.Sprintf("'%s'\n", expectedValue), settingValue)
	}

	l.T().Logf("Create nginx deployment with Longhorn PVC")
	nginxResponse := cloudprovider.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, longhornDefaultStorageClassName)

	// Fetch the name of the created volume from the nginx response.
	nginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(nginxResponse.Spec, nginxSpec)
	require.NoError(l.T(), err)

	// Even though the Longhorn default for number of replicas is 2, Rancher enforces its own default of 3.
	volumeName := nginxSpec.Template.Spec.Volumes[0].Name
	checkReplicasCommand := []string{"kubectl", "-n", longhornNamespace, "get", "volumes.longhorn.io", volumeName, `-o=jsonpath="{.spec.numberOfReplicas}"`}
	settingValue, err := kubectl.Command(l.client, nil, l.cluster.ID, checkReplicasCommand, "")
	require.NoError(l.T(), err)
	require.Equal(l.T(), "\"3\"\n", settingValue)

	// Create a new namespace and a debug pod within it to check the host filesystem for the custom Longhorn data directory.
	// We do this in a separate namespace to ease cleanup.
	createdNamespace, err := namespace_actions.CreateNamespace(l.client, debugNamespace, "{}", nil, nil, l.project)
	require.NoError(l.T(), err)

	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	l.session.RegisterCleanupFunc(func() error {
		return steveClient.SteveType(namespace_actions.NamespaceSteveType).Delete(createdNamespace)
	})

	checkDataPathCommand := []string{"kubectl", "debug", "node/" + workerName, "-n", debugNamespace, "--profile=general", "--image=busybox", "--", "/bin/sh", "-c", "test -d /host/var/lib/longhorn-custom/replicas && test -f /host/var/lib/longhorn-custom/longhorn-disk.cfg"}
	_, err = kubectl.Command(l.client, nil, l.cluster.ID, checkDataPathCommand, "")
	require.NoError(l.T(), err)

	waitForPodCommand := []string{"kubectl", "wait", "--for=jsonpath='{.status.phase}'=Succeeded", "-n", debugNamespace, "pod", "--all"}
	_, err = kubectl.Command(l.client, nil, l.cluster.ID, waitForPodCommand, "")
	require.NoError(l.T(), err)

	debugPods, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(debugNamespace).List(nil)
	require.NoError(l.T(), err)

	podStatus := &corev1.PodStatus{}
	err = steveV1.ConvertToK8sType(debugPods.Data[0].Status, podStatus)
	require.Equal(l.T(), "Succeeded", string(podStatus.Phase))
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornTestSuite))
}

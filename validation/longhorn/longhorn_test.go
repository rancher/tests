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
	"github.com/rancher/shepherd/extensions/kubeconfig"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/cloudprovider"
	namespaceActions "github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

var (
	longhornNamespace                 = "longhorn-system"
	longhornChartName                 = "longhorn"
	longhornProjectName               = "longhorn-test"
	longhornStorageClass              = "longhorn"
	longhornStaticDefaultStorageClass = "longhorn-static"
	debugNamespace                    = namegenerator.AppendRandomString("debug-")
)

type LonghornTestSuite struct {
	suite.Suite
	client            *rancher.Client
	session           *session.Session
	cluster           *clusters.ClusterMeta
	project           *management.Project
	payloadOpts       charts.PayloadOpts
	installedLonghorn bool
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
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, longhornNamespace, longhornChartName)
	require.NoError(l.T(), err)

	if chart.IsAlreadyInstalled {
		l.T().Skip("Skipping installation test because Longhorn is already installed")
	}

	l.T().Logf("Installing Longhorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
	err = charts.InstallLonghornChart(l.client, l.payloadOpts, nil)
	require.NoError(l.T(), err)
	l.installedLonghorn = true
}

func (l *LonghornTestSuite) TestVolumeCreationOnWorkload() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, longhornNamespace, longhornChartName)
	require.NoError(l.T(), err)

	if !chart.IsAlreadyInstalled {
		l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
		err = charts.InstallLonghornChart(l.client, l.payloadOpts, nil)
		require.NoError(l.T(), err)
	}

	l.T().Logf("Create nginx deployment with %s PVC on default namespace", longhornStorageClass)
	nginxResponse := cloudprovider.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, longhornStorageClass)

	err = shepherdCharts.WatchAndWaitDeployments(l.client, l.cluster.ID, namespaces.Default, metav1.ListOptions{})
	require.NoError(l.T(), err)

	kubeConfig, err := kubeconfig.GetKubeconfig(l.client, l.cluster.ID)
	require.NoError(l.T(), err)

	var restConfig *rest.Config
	restConfig, err = (*kubeConfig).ClientConfig()
	require.NoError(l.T(), err)

	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	pods, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(namespaces.Default).List(nil)
	require.NotEmpty(l.T(), pods)
	require.NoError(l.T(), err)

	var podName string
	for _, pod := range pods.Data {
		if strings.Contains(pod.Name, nginxResponse.ObjectMeta.Name) {
			podName = pod.Name
			break
		}
	}

	l.T().Logf("Write to mounted volume on pod [%v]", podName)
	writeToMountedVolume := []string{"touch", "/auto-mnt/test-file"}
	_, err = kubeconfig.KubectlExec(restConfig, podName, namespaces.Default, writeToMountedVolume)
	require.NoError(l.T(), err)

	checkFileOnVolume := []string{"stat", "/auto-mnt/test-file"}
	_, err = kubeconfig.KubectlExec(restConfig, podName, namespaces.Default, checkFileOnVolume)
	require.NoError(l.T(), err)
}

func (l *LonghornTestSuite) TestRBACIntegration() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, longhornNamespace, longhornChartName)
	require.NoError(l.T(), err)

	if !chart.IsAlreadyInstalled {
		l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
		err = charts.InstallLonghornChart(l.client, l.payloadOpts, nil)
		require.NoError(l.T(), err)
	}

	cluster, err := l.client.Management.Cluster.ByID(l.cluster.ID)
	require.NoError(l.T(), err)

	project, namespace, err := projects.CreateProjectAndNamespaceUsingWrangler(l.client, l.cluster.ID)
	require.NoError(l.T(), err)
	l.T().Logf("Created project: %v", project.Name)

	projectUser, projectUserClient, err := rbac.AddUserWithRoleToCluster(l.client, rbac.StandardUser.String(), rbac.ProjectMember.String(), cluster, project)
	require.NoError(l.T(), err)
	l.T().Logf("Created user: %v", projectUser.Username)

	readOnlyUser, readOnlyUserClient, err := rbac.AddUserWithRoleToCluster(l.client, rbac.StandardUser.String(), rbac.ReadOnly.String(), cluster, project)
	require.NoError(l.T(), err)
	l.T().Logf("Created user: %v", readOnlyUser.Username)

	storageClass, err := cloudprovider.GetStorageClass(l.client, l.cluster.ID, longhornStorageClass)
	require.NoError(l.T(), err)

	l.T().Log("Create and delete volume with admin user")
	require.NoError(l.T(), charts.CreateAndDeleteLonghornVolume(l.client, l.cluster.ID, namespace.Name, storageClass))

	l.T().Log("Create and delete volume with project user")
	require.NoError(l.T(), charts.CreateAndDeleteLonghornVolume(projectUserClient, l.cluster.ID, namespace.Name, storageClass))

	l.T().Log("Attempt to create and delete volume with project user on the wrong project")
	require.Error(l.T(), charts.CreateAndDeleteLonghornVolume(projectUserClient, l.cluster.ID, longhornNamespace, storageClass))

	l.T().Log("Attempt to create and delete volume with read-only user")
	require.Error(l.T(), charts.CreateAndDeleteLonghornVolume(readOnlyUserClient, l.cluster.ID, namespace.Name, storageClass))
}

func (l *LonghornTestSuite) TestChartInstallCustomConfig() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, longhornNamespace, longhornChartName)
	require.NoError(l.T(), err)

	// If Longhorn was installed by a previous test on this same session, uninstall it to install it again with custom configuration.
	// If Longhorn was installed previously to this test run, leave it be and skip this test. This way we allow for running the
	// next tests on top of a manually installed Longhorn and avoid accidentally uninstalling something important.
	if chart.IsAlreadyInstalled {
		if l.installedLonghorn {
			l.T().Log("Uninstalling Longhorn that was installed on the previous test.")
			err = charts.UninstallLonghornChart(l.client, longhornNamespace, l.cluster.ID, l.payloadOpts.Host)
			require.NoError(l.T(), err)
		} else {
			l.T().Skip("Skipping installation test because Longhorn is already installed")
		}
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
			labelNodeCommand := []string{"kubectl", "label", "node", node.Hostname, "node.longhorn.io/create-default-disk=true"}
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
		"default-replica-count":                "2",
		"storage-over-provisioning-percentage": "150",
		"create-default-disk-labeled-nodes":    "true",
	}

	for setting, expectedValue := range expectedSettings {
		getSettingCommand := []string{"kubectl", "-n", longhornNamespace, "get", "settings.longhorn.io", setting, `-o=jsonpath='{.value}'`}
		settingValue, err := kubectl.Command(l.client, nil, l.cluster.ID, getSettingCommand, "")
		require.NoError(l.T(), err)
		// The output extracted from kubectl has single quotes and a newline on the end.
		require.Equal(l.T(), fmt.Sprintf("'%s'\n", expectedValue), settingValue)
	}

	// Use the "longhorn-static" storage class so we get the expected number of replicas.
	// Using the "longhorn" storage class will always result in 3 volume replicas.
	l.T().Logf("Create nginx deployment with %s PVC on default namespace", longhornStaticDefaultStorageClass)
	nginxResponse := cloudprovider.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, longhornStaticDefaultStorageClass)

	// Fetch the name of the created volume from the nginx response.
	nginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(nginxResponse.Spec, nginxSpec)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), nginxSpec.Template.Spec.Volumes[0])

	// Even though the Longhorn default for number of replicas is 2, Rancher enforces its own default of 3.
	volumeName := nginxSpec.Template.Spec.Volumes[0].Name
	checkReplicasCommand := []string{"kubectl", "-n", longhornNamespace, "get", "volumes.longhorn.io", volumeName, `-o=jsonpath="{.spec.numberOfReplicas}"`}
	settingValue, err := kubectl.Command(l.client, nil, l.cluster.ID, checkReplicasCommand, "")
	require.NoError(l.T(), err)
	require.Equal(l.T(), fmt.Sprintf("\"%v\"\n", expectedSettings["default-replica-count"]), settingValue)

	// Create a new namespace and a debug pod within it to check the host filesystem for the custom Longhorn data directory.
	// We do this in a separate namespace to ease cleanup.
	l.T().Logf("Create namespace [%v] to check node filesystem with debugger pod", debugNamespace)
	createdNamespace, err := namespaceActions.CreateNamespace(l.client, debugNamespace, "{}", nil, nil, l.project)
	require.NoError(l.T(), err)

	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	l.session.RegisterCleanupFunc(func() error {
		return steveClient.SteveType(namespaceActions.NamespaceSteveType).Delete(createdNamespace)
	})

	checkDataPathCommand := []string{"kubectl", "debug", "node/" + workerName, "-n", debugNamespace, "--profile=general", "--image=busybox", "--", "/bin/sh", "-c", "test -d /host/var/lib/longhorn-custom/replicas && test -f /host/var/lib/longhorn-custom/longhorn-disk.cfg"}
	_, err = kubectl.Command(l.client, nil, l.cluster.ID, checkDataPathCommand, "")
	require.NoError(l.T(), err)

	waitForPodCommand := []string{"kubectl", "wait", "--for=jsonpath='{.status.phase}'=Succeeded", "-n", debugNamespace, "pod", "--all"}
	_, err = kubectl.Command(l.client, nil, l.cluster.ID, waitForPodCommand, "")
	require.NoError(l.T(), err)

	debugPods, err := steveClient.SteveType(pods.PodResourceSteveType).NamespacedSteveClient(debugNamespace).List(nil)
	require.NotEmpty(l.T(), debugPods)
	require.NoError(l.T(), err)

	podStatus := &corev1.PodStatus{}
	err = steveV1.ConvertToK8sType(debugPods.Data[0].Status, podStatus)
	require.NoError(l.T(), err)
	require.Equal(l.T(), "Succeeded", string(podStatus.Phase))
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornTestSuite))
}

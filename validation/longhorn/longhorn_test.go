//go:build validation || pit.daily

package longhorn

import (
	"fmt"
	"slices"
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
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/kubeapi/volumes/persistentvolumeclaims"
	namespaceActions "github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/storage"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/actions/workloads/statefulset"
	"github.com/rancher/tests/interoperability/longhorn"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	longhornStorageClass       = "longhorn"
	longhornStaticStorageClass = "longhorn-static"
	createDefaultDiskNodeLabel = "node.longhorn.io/create-default-disk=true"
)

type LonghornTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	longhornTestConfig longhorn.TestConfig
	cluster            *clusters.ClusterMeta
	project            *management.Project
	payloadOpts        charts.PayloadOpts
	installedLonghorn  bool
}

func (l *LonghornTestSuite) TearDownSuite() {
	l.session.Cleanup()
}

func (l *LonghornTestSuite) SetupSuite() {
	l.session = session.NewSession()

	client, err := rancher.NewClient("", l.session)
	require.NoError(l.T(), err)
	l.client = client

	l.longhornTestConfig = *longhorn.GetLonghornTestConfig()

	l.cluster, err = clusters.NewClusterMeta(client, client.RancherConfig.ClusterName)
	require.NoError(l.T(), err)

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

func (l *LonghornTestSuite) TestChartInstall() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	if chart.IsAlreadyInstalled {
		l.T().Skip("Skipping installation test because Longhorn is already installed")
	}

	l.T().Logf("Installing Longhorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
	err = charts.InstallLonghornChart(l.client, l.payloadOpts, nil)
	require.NoError(l.T(), err)
	l.installedLonghorn = true

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

func (l *LonghornTestSuite) TestRBACIntegration() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
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

	storageClass, err := storage.GetStorageClass(l.client, l.cluster.ID, longhornStorageClass)
	require.NoError(l.T(), err)

	l.T().Log("Create and delete volume with admin user")
	require.NoError(l.T(), storage.CreateAndDeleteVolume(l.client, l.cluster.ID, namespace.Name, storageClass))

	l.T().Log("Create and delete volume with project user")
	require.NoError(l.T(), storage.CreateAndDeleteVolume(projectUserClient, l.cluster.ID, namespace.Name, storageClass))

	l.T().Log("Attempt to create and delete volume with project user on the wrong project")
	require.Error(l.T(), storage.CreateAndDeleteVolume(projectUserClient, l.cluster.ID, charts.LonghornNamespace, storageClass))

	l.T().Log("Attempt to create and delete volume with read-only user")
	require.Error(l.T(), storage.CreateAndDeleteVolume(readOnlyUserClient, l.cluster.ID, namespace.Name, storageClass))
}

func (l *LonghornTestSuite) TestScaleStatefulSetWithPVC() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	if !chart.IsAlreadyInstalled {
		l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, l.payloadOpts.Version, l.project.Name, l.payloadOpts.Namespace)
		err = charts.InstallLonghornChart(l.client, l.payloadOpts, nil)
		require.NoError(l.T(), err)
	}

	namespaceName := namegenerator.AppendRandomString("lhsts")
	namespace, err := namespaceActions.CreateNamespace(l.client, namespaceName, "{}", map[string]string{}, map[string]string{}, l.project)
	require.NoError(l.T(), err)
	l.T().Logf("Created namespace %s", namespaceName)

	podTemplate := pods.CreateContainerAndPodTemplate()
	statefulSet, err := statefulset.CreateStatefulSet(l.client, l.cluster.ID, namespace.Name, podTemplate, 3, true, longhornStorageClass)
	require.NoError(l.T(), err)
	l.T().Logf("Created StetefulSet %s on namespace %s", statefulSet.Name, namespaceName)

	// The template we want will always be the last one on the list.
	volumeSourceName := statefulSet.Spec.VolumeClaimTemplates[len(statefulSet.Spec.VolumeClaimTemplates)-1].Name
	storage.CheckVolumeAllocation(l.T(), l.client, l.cluster.ID, namespace.Name, l.longhornTestConfig.LonghornTestStorageClass, volumeSourceName, storage.MountPath)

	var stetefulSetPodReplicas int32 = 5
	statefulSet.Spec.Replicas = &stetefulSetPodReplicas
	statefulSet, err = statefulset.UpdateStatefulSet(l.client, l.cluster.ID, namespace.Name, statefulSet, true)
	require.NoError(l.T(), err)

	storage.CheckVolumeAllocation(l.T(), l.client, l.cluster.ID, namespace.Name, l.longhornTestConfig.LonghornTestStorageClass, volumeSourceName, storage.MountPath)

	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	pvcBeforeScaling, err := steveClient.SteveType(persistentvolumeclaims.PersistentVolumeClaimType).NamespacedSteveClient(namespace.Name).List(nil)
	require.NoError(l.T(), err)

	stetefulSetPodReplicas = 2
	statefulSet.Spec.Replicas = &stetefulSetPodReplicas
	statefulSet, err = statefulset.UpdateStatefulSet(l.client, l.cluster.ID, namespace.Name, statefulSet, true)
	require.NoError(l.T(), err)

	l.T().Logf("Verifying old volumes still exist")
	volumesAfterScaling, err := steveClient.SteveType(storage.PersistentVolumeEntityType).List(nil)
	require.NoError(l.T(), err)
	var volumeNamesAfterScaling []string
	for _, volume := range volumesAfterScaling.Data {
		volumeNamesAfterScaling = append(volumeNamesAfterScaling, volume.Name)
	}

	var pvcSpec corev1.PersistentVolumeClaimSpec
	for _, pvc := range pvcBeforeScaling.Data {
		err = steveV1.ConvertToK8sType(pvc.Spec, &pvcSpec)
		require.NoError(l.T(), err)
		require.True(l.T(), slices.Contains(volumeNamesAfterScaling, pvcSpec.VolumeName))
	}

	pods, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespace.Name).List(nil)
	require.NoError(l.T(), err)
	require.Equal(l.T(), 2, len(pods.Data))

	err = steveClient.SteveType(shepherdPods.PodResourceSteveType).Delete(&pods.Data[0])
	require.NoError(l.T(), err)

	oldPodVolume, err := storage.GetTargetVolume(pods.Data[0], volumeSourceName)
	require.NoError(l.T(), err)
	l.T().Logf("Deleting pod and checking if the volume bound to PVC %s is successfully reattached", oldPodVolume.PersistentVolumeClaim.ClaimName)

	err = shepherdCharts.WatchAndWaitStatefulSets(l.client, l.cluster.ID, namespace.Name, metav1.ListOptions{
		FieldSelector: "metadata.name=" + statefulSet.Name,
	})
	require.NoError(l.T(), err)

	newPods, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespace.Name).List(nil)
	require.NoError(l.T(), err)
	require.Equal(l.T(), 2, len(newPods.Data))

	for _, pod := range newPods.Data {
		// We are interested in the pod that was created instead of the one that was deleted.
		if pod.Name != pods.Data[1].Name {
			newPodVolume, err := storage.GetTargetVolume(pod, volumeSourceName)
			require.NoError(l.T(), err)
			require.Equal(l.T(), oldPodVolume.PersistentVolumeClaim.ClaimName, newPodVolume.PersistentVolumeClaim.ClaimName)
		}
	}
}

func (l *LonghornTestSuite) TestChartInstallStaticCustomConfig() {
	chart, err := shepherdCharts.GetChartStatus(l.client, l.cluster.ID, charts.LonghornNamespace, charts.LonghornChartName)
	require.NoError(l.T(), err)

	// If Longhorn was installed by a previous test on this same session, uninstall it to install it again with custom configuration.
	// If Longhorn was installed previously to this test run, leave it be and skip this test. This way we allow for running the
	// next tests on top of a manually installed Longhorn and avoid accidentally uninstalling something important.
	if chart.IsAlreadyInstalled {
		if l.installedLonghorn {
			l.T().Log("Uninstalling Longhorn as it was installed on a previous test.")
			err = charts.UninstallLonghornChart(l.client, charts.LonghornNamespace, l.cluster.ID, l.payloadOpts.Host)
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
func TestLonghornTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornTestSuite))
}

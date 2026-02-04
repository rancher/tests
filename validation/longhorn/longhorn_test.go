//go:build validation || pit.daily || pit.harvester.daily

package longhorn

import (
	"slices"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/clusters"
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
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type LonghornTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	longhornTestConfig longhorn.TestConfig
	cluster            *clusters.ClusterMeta
	project            *management.Project
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

		l.T().Logf("Installing Lonhgorn chart in cluster [%v] with latest version [%v] in project [%v] and namespace [%v]", l.cluster.Name, payloadOpts.Version, l.project.Name, payloadOpts.Namespace)
		err = charts.InstallLonghornChart(l.client, payloadOpts, nil)
		require.NoError(l.T(), err)
	}
}

func (l *LonghornTestSuite) TestRBACIntegration() {
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

	storageClass, err := storage.GetStorageClass(l.client, l.cluster.ID, l.longhornTestConfig.LonghornTestStorageClass)
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
	namespaceName := namegenerator.AppendRandomString("lhsts")
	namespace, err := namespaceActions.CreateNamespace(l.client, namespaceName, "{}", map[string]string{}, map[string]string{}, l.project)
	require.NoError(l.T(), err)
	l.T().Logf("Created namespace %s", namespaceName)

	podTemplate := pods.CreateContainerAndPodTemplate()
	statefulSet, err := statefulset.CreateStatefulSet(l.client, l.cluster.ID, namespace.Name, podTemplate, 3, true, l.longhornTestConfig.LonghornTestStorageClass)
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

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornTestSuite))
}

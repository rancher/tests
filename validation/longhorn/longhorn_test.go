//go:build (validation || pit.daily || pit.elemental || pit.harvester.daily) && !airgap.daily

package longhorn

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"slices"
	"testing"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherdCharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/kubeapi/secrets"
	extstatefulsetapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/statefulsets"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	shepherdPods "github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	longhornActions "github.com/rancher/tests/actions/kubeapi/longhorn"
	projectapi "github.com/rancher/tests/actions/kubeapi/projects"
	"github.com/rancher/tests/actions/kubeapi/storageclasses"
	"github.com/rancher/tests/actions/kubeapi/volumes/persistentvolumeclaims"
	podapi "github.com/rancher/tests/actions/kubeapi/workloads/pods"
	statefulsetapi "github.com/rancher/tests/actions/kubeapi/workloads/statefulsets"
	namespaceActions "github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/storage"
	s3Actions "github.com/rancher/tests/actions/storage/s3"
	"github.com/rancher/tests/interoperability/longhorn"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	appv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	v1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var (
	LonghornEncryptionSecretBaseName       = "longhorn-crypto"
	LonghornEncryptionStorageClassBaseName = "longhorn-crypto-global"
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

	l.longhornTestConfig = longhorn.GetLonghornTestConfig()

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

	project, namespace, err := projectapi.CreateProjectAndNamespace(l.client, l.cluster.ID)
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
	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	nodeList, err := steveClient.SteveType("node").List(nil)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), nodeList.Data)

	nodeCount := len(nodeList.Data)

	minStatefulSetReplicas := int32(1)
	maxStatefulSetReplicas := int32(nodeCount + 1)

	namespaceName := namegenerator.AppendRandomString("lhsts")
	namespace, err := namespaceActions.CreateNamespace(l.client, namespaceName, "{}", map[string]string{}, map[string]string{}, l.project)
	require.NoError(l.T(), err)
	l.T().Logf("Created namespace %s", namespaceName)

	podTemplate := podapi.CreateContainerAndPodTemplate("")
	statefulSet, err := statefulsetapi.CreateStatefulSet(l.client, l.cluster.ID, namespace.Name, podTemplate, minStatefulSetReplicas, true, l.longhornTestConfig.LonghornTestStorageClass)
	require.NoError(l.T(), err)
	l.T().Logf("Created StetefulSet %s on namespace %s", statefulSet.Name, namespaceName)

	// The template we want will always be the last one on the list.
	volumeSourceName := statefulSet.Spec.VolumeClaimTemplates[len(statefulSet.Spec.VolumeClaimTemplates)-1].Name
	storage.CheckVolumeAllocation(l.T(), l.client, l.cluster.ID, namespace.Name, l.longhornTestConfig.LonghornTestStorageClass, volumeSourceName, storage.MountPath)

	statefulSet.Spec.Replicas = &maxStatefulSetReplicas
	err = charts.RetryOnWatchError(charts.DefaultWatchRetries, func() error {
		statefulSet, err = extstatefulsetapi.UpdateStatefulSet(l.client, l.cluster.ID, statefulSet, true)
		return err
	})
	require.NoError(l.T(), err)

	err = shepherdCharts.WatchAndWaitStatefulSets(l.client, l.cluster.ID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + statefulSet.Name,
	})
	require.NoError(l.T(), err)

	storage.CheckVolumeAllocation(l.T(), l.client, l.cluster.ID, namespace.Name, l.longhornTestConfig.LonghornTestStorageClass, volumeSourceName, storage.MountPath)

	pvcBeforeScaling, err := steveClient.SteveType(persistentvolumeclaims.PersistentVolumeClaimType).NamespacedSteveClient(namespace.Name).List(nil)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), pvcBeforeScaling.Data)

	statefulSet.Spec.Replicas = &minStatefulSetReplicas
	statefulSet, err = extstatefulsetapi.UpdateStatefulSet(l.client, l.cluster.ID, statefulSet, true)
	require.NoError(l.T(), err)

	l.T().Logf("Verifying old volumes still exist")
	volumesAfterScaling, err := steveClient.SteveType(storage.PersistentVolumeEntityType).List(nil)
	require.NoError(l.T(), err)
	require.NotEmpty(l.T(), volumesAfterScaling.Data)

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

	err = shepherdCharts.WatchAndWaitStatefulSets(l.client, l.cluster.ID, namespaceName, metav1.ListOptions{
		FieldSelector: "metadata.name=" + statefulSet.Name,
	})
	require.NoError(l.T(), err)

	podList, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespace.Name).List(nil)
	require.NoError(l.T(), err)
	require.Equal(l.T(), int(minStatefulSetReplicas), len(podList.Data))

	err = steveClient.SteveType(shepherdPods.PodResourceSteveType).Delete(&podList.Data[0])
	require.NoError(l.T(), err)

	oldPodVolume, err := storage.GetTargetVolume(podList.Data[0], volumeSourceName)
	require.NoError(l.T(), err)
	l.T().Logf("Deleting pod and checking if the volume bound to PVC %s is successfully reattached", oldPodVolume.PersistentVolumeClaim.ClaimName)

	err = shepherdCharts.WatchAndWaitStatefulSets(l.client, l.cluster.ID, namespace.Name, metav1.ListOptions{
		FieldSelector: "metadata.name=" + statefulSet.Name,
	})
	require.NoError(l.T(), err)

	newPodList, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespace.Name).List(nil)
	require.NoError(l.T(), err)
	require.Equal(l.T(), int(minStatefulSetReplicas), len(newPodList.Data))

	for _, pod := range newPodList.Data {
		// We are interested in the pod that was created instead of the one that was deleted.
		if pod.Name != podList.Data[0].Name {
			newPodVolume, err := storage.GetTargetVolume(pod, volumeSourceName)
			require.NoError(l.T(), err)
			require.Equal(l.T(), oldPodVolume.PersistentVolumeClaim.ClaimName, newPodVolume.PersistentVolumeClaim.ClaimName)
		}
	}
}

func (l *LonghornTestSuite) TestVolumeEncryption() {
	// Try to get AWS credentials first to fail early in case they are not provided.
	var awsCreds cloudcredentials.AmazonEC2CredentialConfig
	operations.LoadObjectFromMap(cloudcredentials.AmazonEC2CredentialConfigurationFileKey, config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey)), &awsCreds)

	steveClient, err := l.client.Steve.ProxyDownstream(l.cluster.ID)
	require.NoError(l.T(), err)

	key := make([]byte, 64)
	rand.Read(key)

	secretName := namegenerator.AppendRandomString(LonghornEncryptionSecretBaseName)
	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: charts.LonghornNamespace,
		},
		StringData: map[string]string{
			"CRYPTO_KEY_VALUE":    string(key),
			"CRYPTO_KEY_PROVIDER": "secret",
		},
	}

	_, err = secrets.CreateSecretWithTemplate(l.client, l.cluster.ID, secret)
	require.NoError(l.T(), err)
	l.T().Logf("Created %s secret containing crypto key", secretName)

	// Parameters taken from example on docs: https://longhorn.io/docs/archives/1.3.0/advanced-resources/security/volume-encryption/#setting-up-kubernetes-secrets
	parametersMap := map[string]string{
		"numberOfReplicas":    "3",
		"staleReplicaTimeout": "2880",
		"fromBackup":          "",
		"encrypted":           "true",
		"csi.storage.k8s.io/provisioner-secret-name":       secretName,
		"csi.storage.k8s.io/provisioner-secret-namespace":  "longhorn-system",
		"csi.storage.k8s.io/node-publish-secret-name":      secretName,
		"csi.storage.k8s.io/node-publish-secret-namespace": "longhorn-system",
		"csi.storage.k8s.io/node-stage-secret-name":        secretName,
		"csi.storage.k8s.io/node-stage-secret-namespace":   "longhorn-system",
	}

	storageClassName := namegenerator.AppendRandomString(LonghornEncryptionStorageClassBaseName)
	l.T().Logf("Create %s Storage Class with encryption", storageClassName)
	storageClass := storageclasses.NewStorageClass(storageClassName, "", longhornActions.LonghornStorageClassProvisioner, true, parametersMap, []string{}, "", v1.VolumeBindingImmediate)

	_, err = steveClient.SteveType(storage.StorageClassSteveType).Create(storageClass)
	require.NoError(l.T(), err)

	l.T().Logf("Create nginx deployment with %s PVC on default namespace", storageClassName)
	nginxResponse := storage.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, storageClassName)

	nginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(nginxResponse.Spec, nginxSpec)
	require.NoError(l.T(), err)
	require.Equal(l.T(), len(nginxSpec.Template.Spec.Volumes), 1)

	volumeName := nginxSpec.Template.Spec.Volumes[0].Name

	l.T().Logf("Check 'encrypted' parameter on volume %s is set to 'true'", volumeName)
	volume, err := storage.GetPersistentVolumeByName(l.client, l.cluster.ID, volumeName)
	require.NoError(l.T(), err)
	require.Equal(l.T(), volume.Spec.CSI.VolumeAttributes["encrypted"], "true")

	labelSelector := fmt.Sprintf("labelSelector=%s=%s", storage.DeploymentIdentifierLabel, nginxResponse.Name)
	query, _ := url.ParseQuery(labelSelector)

	pods, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespaces.Default).List(query)
	require.NotEmpty(l.T(), pods)
	require.NoError(l.T(), err)

	var pod corev1.Pod
	err = steveV1.ConvertToK8sType(pods.Data[0], &pod)
	require.NoError(l.T(), err)

	kubeConfig, err := kubeconfig.GetKubeconfig(l.client, l.cluster.ID)
	require.NoError(l.T(), err)

	storage.CheckMountedVolume(l.T(), kubeConfig, l.cluster.ID, namespaces.Default, pod.Name, storage.MountPath)

	saltBytes := make([]byte, 20)
	rand.Read(saltBytes)
	salt := hex.EncodeToString(saltBytes)

	testFileName := namegenerator.AppendRandomString("test-file" + volumeName)
	l.T().Logf("Writing salt %s on mouted volume under the path [%v] on pod [%v]", salt, storage.MountPath+"/"+testFileName, pod.Name)
	writeToMountedVolume := []string{"echo", salt, ">", storage.MountPath + "/" + testFileName}

	restConfig, err := (*kubeConfig).ClientConfig()
	require.NoError(l.T(), err)

	output, err := kubeconfig.KubectlExec(restConfig, pod.Name, namespaces.Default, writeToMountedVolume)
	if err != nil {
		l.T().Logf("Command failed on pod %s: %s", pod.Name, output)
	}
	require.NoError(l.T(), err)

	nodeCollection, err := l.client.Management.Node.List(&types.ListOpts{Filters: map[string]interface{}{
		"clusterId": l.cluster.ID,
		"name":      pod.Spec.NodeName,
	}})
	require.NoError(l.T(), err)
	require.Equal(l.T(), len(nodeCollection.Data), 1)
	nodeName := nodeCollection.Data[0].Name

	l.T().Logf("Searching for text plain salt %s node %s's filesystem", salt, nodeName)
	// When running grep, return on first match, search recursively and negate the exit code.
	checkPlainTextContent := fmt.Sprintf("stat /host/var/lib/longhorn/replicas/%s* && ! grep -qr /host/var/lib/longhorn/replicas/%s* -e '%s'", volumeName, volumeName, salt)
	storage.CheckNodeFilesystem(l.T(), l.client, l.cluster.ID, nodeName, checkPlainTextContent, l.project)

	region := awsCreds.DefaultRegion
	if region == "" {
		region = "us-east-2"
	}

	bucketName := namegenerator.AppendRandomString("pit-longhorn")
	err = s3Actions.CreateS3Bucket(bucketName, region, awsCreds.AccessKey, awsCreds.SecretKey)
	require.NoError(l.T(), err)
	l.T().Logf("Created S3 bucket %s to use as backup target", bucketName)

	l.client.Session.RegisterCleanupFunc(func() error {
		return s3Actions.DeleteS3Bucket(bucketName, region, awsCreds.AccessKey, awsCreds.SecretKey)
	})

	target, err := longhornActions.CreateS3LonghornBackupTarget(l.client, l.cluster.ID, awsCreds, region, bucketName)
	require.NoError(l.T(), err)
	l.T().Logf("Created backup target %s on S3 bucket %s", target.Name, bucketName)

	l.T().Logf("Backing up volume %s on S3 bucket %s", volumeName, bucketName)
	backup, err := longhornActions.CreateLonghornVolumeBackup(l.client, l.cluster.ID, charts.LonghornNamespace, volumeName, target.Name)
	require.NoError(l.T(), err)

	objectKey, err := s3Actions.FindBackupS3ObjectKey(awsCreds, region, bucketName, volumeName)
	require.NoError(l.T(), err)

	blockBytes, err := s3Actions.ReadS3Object(awsCreds, region, bucketName, objectKey)
	require.NoError(l.T(), err)
	l.T().Logf("Checking for salt byte (%s) matches on backup on %s", salt, objectKey)
	require.False(l.T(), bytes.Contains(blockBytes, saltBytes))

	l.T().Log("Restoring volume from backup")
	err = longhornActions.RestoreLonghornVolumeFromBackup(l.client, l.cluster.ID, *backup)
	require.NoError(l.T(), err)

	l.T().Logf("Updating secret %s with new key value", secretName)
	rand.Read(key)
	secret.StringData["CRYPTO_KEY_VALUE"] = string(key)

	err = secrets.UpdateSecretWithTemplate(l.client, l.cluster.ID, secret)
	require.NoError(l.T(), err)

	l.T().Logf("Creating deployment with encrypted Longhorn PVC after updating encryption secret")
	secondNginxResponse := storage.CreatePVCWorkload(l.T(), l.client, l.cluster.ID, storageClassName)

	secondNginxSpec := &appv1.DeploymentSpec{}
	err = steveV1.ConvertToK8sType(secondNginxResponse.Spec, secondNginxSpec)
	require.NoError(l.T(), err)
	require.Equal(l.T(), len(secondNginxSpec.Template.Spec.Volumes), 1)

	secondVolumeName := secondNginxSpec.Template.Spec.Volumes[0].Name

	l.T().Logf("Validating that the new volume %s is marked as 'encrypted'", secondVolumeName)
	secondVolume, err := storage.GetPersistentVolumeByName(l.client, l.cluster.ID, secondVolumeName)
	require.NoError(l.T(), err)
	require.Equal(l.T(), secondVolume.Spec.CSI.VolumeAttributes["encrypted"], "true")

	labelSelector = fmt.Sprintf("labelSelector=%s=%s", storage.DeploymentIdentifierLabel, nginxResponse.Name)
	query, _ = url.ParseQuery(labelSelector)

	pods, err = steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(namespaces.Default).List(query)
	require.NotEmpty(l.T(), pods)
	require.NoError(l.T(), err)

	var otherPod corev1.Pod
	err = steveV1.ConvertToK8sType(pods.Data[0], &otherPod)
	require.NoError(l.T(), err)

	l.T().Logf("Validating that the old volume %s and the new volume %s are writable", volumeName, secondVolumeName)
	storage.CheckMountedVolume(l.T(), kubeConfig, l.cluster.ID, namespaces.Default, otherPod.Name, storage.MountPath)
	storage.CheckMountedVolume(l.T(), kubeConfig, l.cluster.ID, namespaces.Default, pod.Name, storage.MountPath)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestLonghornTestSuite(t *testing.T) {
	suite.Run(t, new(LonghornTestSuite))
}

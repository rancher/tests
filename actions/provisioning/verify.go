package provisioning

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"

	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioninginput"
	wranglername "github.com/rancher/wrangler/pkg/name"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	shepherdclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/bundledclusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/kubeconfig"
	nodestat "github.com/rancher/shepherd/extensions/nodes"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/wait"
	psadeploy "github.com/rancher/tests/actions/psact"
	"github.com/rancher/tests/actions/registries"
	"github.com/rancher/tests/actions/reports"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/apimachinery/pkg/watch"
	capi "sigs.k8s.io/cluster-api/api/v1beta1"
)

const (
	local                       = "local"
	logMessageKubernetesVersion = "Validating the current version is the upgraded one"
	hostnameLimit               = 63
	etcdSnapshotAnnotation      = "etcdsnapshot.rke.io/storage"
	machineNameAnnotation       = "cluster.x-k8s.io/machine"
	deploymentNameLabel         = "cluster.x-k8s.io/deployment-name"
	onDemandPrefix              = "on-demand-"
	s3                          = "s3"
	DefaultRancherDataDir       = "/var/lib/rancher"
	oneSecondInterval           = time.Duration(1 * time.Second)
	notFound                    = "404 Not Found"
)

// VerifyRKE1Cluster validates that the RKE1 cluster and its resources are in a good state, matching a given config.
func VerifyRKE1Cluster(t *testing.T, client *rancher.Client, clustersConfig *clusters.ClusterConfig, cluster *management.Cluster) {
	client, err := client.ReLogin()
	require.NoError(t, err)

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	watchInterface, err := adminClient.GetManagementWatchInterface(management.ClusterType, metav1.ListOptions{
		FieldSelector:  "metadata.name=" + cluster.ID,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	require.NoError(t, err)

	checkFunc := shepherdclusters.IsHostedProvisioningClusterReady
	err = wait.WatchWait(watchInterface, checkFunc)
	require.NoError(t, err)

	require.Equal(t, clustersConfig.KubernetesVersion, cluster.RancherKubernetesEngineConfig.Version)

	clusterToken, err := clusters.CheckServiceAccountTokenSecret(client, cluster.Name)
	reports.TimeoutRKEReport(cluster, err)
	require.NoError(t, err)
	require.NotEmpty(t, clusterToken)

	err = nodestat.AllManagementNodeReady(client, cluster.ID, defaults.ThirtyMinuteTimeout)
	reports.TimeoutRKEReport(cluster, err)
	require.NoError(t, err)

	if clustersConfig.PSACT == string(provisioninginput.RancherPrivileged) || clustersConfig.PSACT == string(provisioninginput.RancherRestricted) || clustersConfig.PSACT == string(provisioninginput.RancherBaseline) {
		require.NotEmpty(t, cluster.DefaultPodSecurityAdmissionConfigurationTemplateName)

		err := psadeploy.CreateNginxDeployment(client, cluster.ID, clustersConfig.PSACT)
		reports.TimeoutRKEReport(cluster, err)
		require.NoError(t, err)
	}
	if clustersConfig.Registries != nil {
		if clustersConfig.Registries.RKE1Registries != nil {
			for _, registry := range clustersConfig.Registries.RKE1Registries {
				havePrefix, err := registries.CheckAllClusterPodsForRegistryPrefix(client, cluster.ID, registry.URL)
				reports.TimeoutRKEReport(cluster, err)
				require.NoError(t, err)
				require.True(t, havePrefix)
			}
		}
	}
	if clustersConfig.Networking != nil {
		if clustersConfig.Networking.LocalClusterAuthEndpoint != nil {
			cluster, err := adminClient.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetDefault + "/" + cluster.Name)
			require.NoError(t, err)

			VerifyACE(t, adminClient, cluster)
		}
	}

	if clustersConfig.CloudProvider == "" {
		podErrors := pods.StatusPods(client, cluster.ID)
		require.Empty(t, podErrors)
	}
}

// VerifyClusterReady validates that a non-rke1 cluster and its resources are in a good state, matching a given config.
func VerifyClusterReady(client *rancher.Client, cluster *steveV1.SteveAPIObject) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, defaults.FifteenMinuteTimeout, false, func(context.Context) (done bool, err error) {
		adminClient, err := client.ReLogin()
		if err != nil {
			logrus.Warningf("Unable to get cluster client (%s) retrying", cluster.Name)
			return false, nil
		}

		kubeProvisioningClient, err := adminClient.GetKubeAPIProvisioningClient()
		if err != nil {
			logrus.Warningf("Unable to get cluster kube client (%s) retrying", cluster.Name)
			return false, nil
		}

		watchInterface, err := kubeProvisioningClient.Clusters(namespaces.FleetDefault).Watch(context.TODO(), metav1.ListOptions{
			FieldSelector:  "metadata.name=" + cluster.Name,
			TimeoutSeconds: &defaults.WatchTimeoutSeconds,
		})
		if err != nil {
			return false, nil
		}

		checkFunc := shepherdclusters.IsProvisioningClusterReady
		err = wait.WatchWait(watchInterface, checkFunc)
		if err != nil {
			logrus.Warningf("Unable to get cluster status (%s) Retrying", cluster.Name)
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		return err
	}

	logrus.Debugf("Waiting for all machines to be ready on cluster (%s)", cluster.Name)
	err = nodestat.AllMachineReady(client, cluster.ID, defaults.FiveMinuteTimeout)
	if err != nil {
		return err
	}

	logrus.Debugf("Verifying cluster token (%s)", cluster.Name)
	clusterToken, err := clusters.CheckServiceAccountTokenSecret(client, cluster.Name)
	if err != nil {
		return err
	}

	if clusterToken != true {
		return fmt.Errorf("cluster token is not valid")
	}

	return nil
}

func VerifyPSACT(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	clusterSpec := &provv1.ClusterSpec{}
	err = steveV1.ConvertToK8sType(cluster.Spec, clusterSpec)
	require.NoError(t, err)
	require.NotEmpty(t, clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName)

	err = psadeploy.CreateNginxDeployment(client, status.ClusterName, clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName)
	require.NoError(t, err)
}

// VerifyCluster validates that a non-rke1 cluster and its resources are in a good state, matching a given config.
func VerifyDynamicCluster(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) {
	client, err := client.ReLogin()
	require.NoError(t, err)

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	status := &provv1.ClusterStatus{}
	err = steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	clusterSpec := &provv1.ClusterSpec{}
	err = steveV1.ConvertToK8sType(cluster.Spec, clusterSpec)
	require.NoError(t, err)

	isRancherPrivilaged := clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName == string(provisioninginput.RancherPrivileged)
	isRancherRestricted := clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName == string(provisioninginput.RancherRestricted)
	isRancherBaseline := clusterSpec.DefaultPodSecurityAdmissionConfigurationTemplateName == string(provisioninginput.RancherBaseline)
	if isRancherPrivilaged || isRancherRestricted || isRancherBaseline {
		VerifyPSACT(t, client, cluster)
	}

	if clusterSpec.RKEConfig.Registries != nil {
		for registryName := range clusterSpec.RKEConfig.Registries.Configs {
			havePrefix, err := registries.CheckAllClusterPodsForRegistryPrefix(client, status.ClusterName, registryName)
			require.NoError(t, err)
			require.True(t, havePrefix)
		}
	}

	if clusterSpec.LocalClusterAuthEndpoint.Enabled {
		VerifyACE(t, adminClient, cluster)
	}
}

// VerifyHostedCluster validates that the hosted cluster and its resources are in a good state, matching a given config.
func VerifyHostedCluster(t *testing.T, client *rancher.Client, cluster *management.Cluster) {
	client, err := client.ReLogin()
	require.NoError(t, err)

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	watchInterface, err := adminClient.GetManagementWatchInterface(management.ClusterType, metav1.ListOptions{
		FieldSelector:  "metadata.name=" + cluster.ID,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	reports.TimeoutRKEReport(cluster, err)
	require.NoError(t, err)

	checkFunc := shepherdclusters.IsHostedProvisioningClusterReady

	err = wait.WatchWait(watchInterface, checkFunc)
	reports.TimeoutRKEReport(cluster, err)
	require.NoError(t, err)

	clusterToken, err := clusters.CheckServiceAccountTokenSecret(client, cluster.Name)
	reports.TimeoutRKEReport(cluster, err)
	require.NoError(t, err)
	require.NotEmpty(t, clusterToken)

	err = nodestat.AllManagementNodeReady(client, cluster.ID, defaults.ThirtyMinuteTimeout)
	reports.TimeoutRKEReport(cluster, err)
	require.NoError(t, err)

	podErrors := pods.StatusPods(client, cluster.ID)
	require.Empty(t, podErrors)
}

// VerifyDeleteRKE1Cluster validates that a rke1 cluster and its resources are deleted.
func VerifyDeleteRKE1Cluster(t *testing.T, client *rancher.Client, clusterID string) {
	cluster, err := client.Management.Cluster.ByID(clusterID)
	require.NoError(t, err)

	adminClient, err := rancher.NewClient(client.RancherConfig.AdminToken, client.Session)
	require.NoError(t, err)

	watchInterface, err := adminClient.GetManagementWatchInterface(management.ClusterType, metav1.ListOptions{
		FieldSelector:  "metadata.name=" + clusterID,
		TimeoutSeconds: &defaults.WatchTimeoutSeconds,
	})
	require.NoError(t, err)

	err = wait.WatchWait(watchInterface, func(event watch.Event) (ready bool, err error) {
		if event.Type == watch.Error {
			return false, fmt.Errorf("error: unable to delete cluster %s", cluster.Name)
		} else if event.Type == watch.Deleted {
			logrus.Infof("Cluster %s deleted!", cluster.Name)
			return true, nil
		}
		return false, nil
	})
	require.NoError(t, err)

	err = nodestat.AllNodeDeleted(client, clusterID)
	require.NoError(t, err)
}

// VerifyDeleteRKE2K3SCluster validates that a non-rke1 cluster and its resources are deleted.
func VerifyDeleteRKE2K3SCluster(t *testing.T, client *rancher.Client, clusterID string) {
	logrus.Debugf("Waiting for cluster (%s) to be deleted", clusterID)
	ctx := context.Background()
	err := kwait.PollUntilContextTimeout(
		ctx, oneSecondInterval, defaults.TenMinuteTimeout, true, func(ctx context.Context) (bool, error) {
			_, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
			if err != nil {
				if strings.Contains(err.Error(), notFound) {
					return true, nil
				}

				return false, err
			}

			return false, nil
		})
	require.NoError(t, err)

	logrus.Infof("Waiting for nodes to be deleted on cluster (%s)", clusterID)
	err = nodestat.AllNodeDeleted(client, clusterID)
	require.NoError(t, err)
}

// VerifyACE validates that the ACE resources are healthy in a given cluster
func VerifyACE(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) {
	status := &provv1.ClusterStatus{}
	err := steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	clusterObject, err := client.Management.Cluster.ByID(status.ClusterName)
	require.NoError(t, err)

	client, err = client.ReLogin()
	require.NoError(t, err)

	kubeConfig, err := kubeconfig.GetKubeconfig(client, clusterObject.ID)
	require.NoError(t, err)

	original, err := client.SwitchContext(clusterObject.Name, kubeConfig)
	require.NoError(t, err)

	originalResp, err := original.Resource(corev1.SchemeGroupVersion.WithResource("pods")).Namespace("").List(context.TODO(), metav1.ListOptions{})
	require.NoError(t, err)

	for _, pod := range originalResp.Items {
		logrus.Debugf("Pod %v", pod.GetName())
	}

	// each control plane has a context. For ACE, we should check these contexts
	contexts, err := kubeconfig.GetContexts(kubeConfig)
	require.NoError(t, err)

	var contextNames []string
	for context := range contexts {
		if strings.Contains(context, "pool") {
			contextNames = append(contextNames, context)
		}
	}

	for _, contextName := range contextNames {
		dynamic, err := client.SwitchContext(contextName, kubeConfig)
		require.NoError(t, err)

		resp, err := dynamic.Resource(corev1.SchemeGroupVersion.WithResource("pods")).Namespace("").List(context.TODO(), metav1.ListOptions{})
		require.NoError(t, err)

		logrus.Infof("Switched Context to %v", contextName)
		for _, pod := range resp.Items {
			logrus.Debugf("Pod %v", pod.GetName())
		}
	}
}

// VerifyHostnameLength validates that the hostnames of the nodes in a cluster are of the correct length
func VerifyHostnameLength(t *testing.T, client *rancher.Client, clusterObject *steveV1.SteveAPIObject) {
	clusterSpec := &provv1.ClusterSpec{}
	err := steveV1.ConvertToK8sType(clusterObject.Spec, clusterSpec)
	require.NoError(t, err)

	for _, mp := range clusterSpec.RKEConfig.MachinePools {
		machineName := wranglername.SafeConcatName(clusterObject.Name, mp.Name)

		machineResp, err := client.Steve.SteveType(stevetypes.Machine).List(nil)
		require.NoError(t, err)

		var machinePool *steveV1.SteveAPIObject
		for _, machine := range machineResp.Data {
			if machine.Labels[deploymentNameLabel] == machineName {
				machinePool = &machine
			}
		}
		require.NotNil(t, machinePool)

		capiMachine := capi.Machine{}
		err = steveV1.ConvertToK8sType(machinePool.JSONResp, &capiMachine)
		require.NoError(t, err)
		require.NotNil(t, capiMachine.Status.NodeRef)

		dynamic, err := client.GetRancherDynamicClient()
		require.NoError(t, err)

		gv, err := schema.ParseGroupVersion(capiMachine.Spec.InfrastructureRef.APIVersion)
		require.NoError(t, err)

		gvr := schema.GroupVersionResource{
			Group:    gv.Group,
			Version:  gv.Version,
			Resource: strings.ToLower(capiMachine.Spec.InfrastructureRef.Kind) + "s",
		}

		ustr, err := dynamic.Resource(gvr).Namespace(capiMachine.Namespace).Get(context.TODO(), capiMachine.Spec.InfrastructureRef.Name, metav1.GetOptions{})
		require.NoError(t, err)

		limit := hostnameLimit
		if mp.HostnameLengthLimit != 0 {
			limit = mp.HostnameLengthLimit
		} else if clusterSpec.RKEConfig.MachinePoolDefaults.HostnameLengthLimit != 0 {
			limit = clusterSpec.RKEConfig.MachinePoolDefaults.HostnameLengthLimit
		}

		require.True(t, len(capiMachine.Status.NodeRef.Name) <= limit)
		if len(ustr.GetName()) < limit {
			require.True(t, capiMachine.Status.NodeRef.Name == ustr.GetName())
		}

		logrus.Debugf("Hostname: %s, HostnameLimit: %v", capiMachine.Status.NodeRef.Name, limit)
	}
}

// VerifyUpgrade validates that a cluster has been upgraded to a given version
func VerifyUpgrade(t *testing.T, updatedCluster *bundledclusters.BundledCluster, upgradedVersion string) {
	if updatedCluster.V3 != nil {
		assert.Equalf(t, upgradedVersion, updatedCluster.V3.RancherKubernetesEngineConfig.Version, "[%v]: %v", updatedCluster.Meta.Name, logMessageKubernetesVersion)
	} else {
		clusterSpec := &provv1.ClusterSpec{}
		err := steveV1.ConvertToK8sType(updatedCluster.V1.Spec, clusterSpec)
		require.NoError(t, err)
		assert.Equalf(t, upgradedVersion, clusterSpec.KubernetesVersion, "[%v]: %v", updatedCluster.Meta.Name, logMessageKubernetesVersion)
	}
}

// VerifyDataDirectories validates that data is being distributed properly across data directories.
func VerifyDataDirectories(t *testing.T, client *rancher.Client, cluster *steveV1.SteveAPIObject) {
	clusterSpec := &provv1.ClusterSpec{}
	err := steveV1.ConvertToK8sType(cluster.Spec, clusterSpec)
	require.NoError(t, err)
	require.NotNil(t, clusterSpec.RKEConfig.DataDirectories)

	client, err = client.ReLogin()
	require.NoError(t, err)

	status := &provv1.ClusterStatus{}
	err = steveV1.ConvertToK8sType(cluster.Status, status)
	require.NoError(t, err)

	steveClient, err := client.Steve.ProxyDownstream(status.ClusterName)
	require.NoError(t, err)

	nodesSteveObjList, err := steveClient.SteveType(stevetypes.Node).List(nil)
	require.NoError(t, err)

	for _, machine := range nodesSteveObjList.Data {
		clusterNode, err := sshkeys.GetSSHNodeFromMachine(client, &machine)
		require.NoError(t, err)

		_, err = clusterNode.ExecuteCommand(fmt.Sprintf("sudo ls %s", clusterSpec.RKEConfig.DataDirectories.K8sDistro))
		assert.NoError(t, err)
		logrus.Debugf("Verified k8sDistro directory(%s) on node(%s)", clusterSpec.RKEConfig.DataDirectories.K8sDistro, clusterNode.NodeID)

		_, err = clusterNode.ExecuteCommand(fmt.Sprintf("sudo ls %s", clusterSpec.RKEConfig.DataDirectories.Provisioning))
		assert.NoError(t, err)
		logrus.Debugf("Verified provisioning directory(%s) on node(%s)", clusterSpec.RKEConfig.DataDirectories.Provisioning, clusterNode.NodeID)

		_, err = clusterNode.ExecuteCommand(fmt.Sprintf("sudo ls %s", clusterSpec.RKEConfig.DataDirectories.SystemAgent))
		assert.NoError(t, err)
		logrus.Debugf("Verified systemAgent directory(%s) on node(%s)", clusterSpec.RKEConfig.DataDirectories.SystemAgent, clusterNode.NodeID)

		_, err = clusterNode.ExecuteCommand(fmt.Sprintf("sudo ls %s", DefaultRancherDataDir))
		assert.Error(t, err)
		logrus.Debugf("Verified that the default data directory(%s) on node(%s) does not exist", clusterSpec.RKEConfig.DataDirectories.SystemAgent, clusterNode.NodeID)
	}
}

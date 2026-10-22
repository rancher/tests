//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"fmt"
	"strings"

	fleetv1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionClusters "github.com/rancher/shepherd/extensions/clusters"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/interoperability/fleet"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	// fleetGitRepoName is the deterministic GitRepo name created in the
	// fleet-default namespace by the pre-upgrade phase and reconciled by the
	// post-upgrade phase.
	fleetGitRepoName = "rancher-upgrade-fleet-gitrepo"

	// fleetSSHSecretName is the deterministic SSH auth secret, in the
	// fleet-default namespace, used by the fleet-agent to fetch from the
	// node-local git remote.
	fleetSSHSecretName = "rancher-upgrade-fleet-ssh"

	// fleetGitRepoDir is the clone directory under the SSH node's home
	// directory; it doubles as the git remote the GitRepo points at. Its name
	// must be deterministic because the post-upgrade phase pushes into the
	// clone created by the pre-upgrade phase.
	fleetGitRepoDir = "rancher-upgrade-fleet-repo"

	// fleetTargetNamespace is the deterministic namespace, on the target
	// cluster, that receives the example bundle's workloads.
	fleetTargetNamespace = "rancher-upgrade-fleet-target"

	// fleetStateConfigMapName is the ConfigMap, in the fleet-default
	// namespace, holding the pre-upgrade observations the post-upgrade phase
	// asserts against.
	fleetStateConfigMapName = "rancher-upgrade-fleet-state"

	// fleetStateKeyControllerVersion records the pre-upgrade fleet-controller
	// image version.
	fleetStateKeyControllerVersion = "fleetControllerVersion"

	// fleetStateKeyAgentVersion records the pre-upgrade fleet-agent image
	// version.
	fleetStateKeyAgentVersion = "fleetAgentVersion"

	// fleetStateKeyCommit records the commit SHA the GitRepo had synced
	// before the Rancher server upgrade.
	fleetStateKeyCommit = "gitCommit"

	// fleetAgentLocalDeployment is the steve deployment ID of the fleet-agent
	// when the target cluster is the local cluster, where the agent runs in
	// cattle-fleet-local-system.
	fleetAgentLocalDeployment = "cattle-fleet-local-system/fleet-agent"

	// fleetAgentDownstreamDeployment is the steve deployment ID of the
	// fleet-agent on a non-local target cluster.
	fleetAgentDownstreamDeployment = "cattle-fleet-system/fleet-agent"
)

// fleetStateConfigMapLabels labels the cross-phase state ConfigMap so the
// rancher-upgrade verticals' state resources can be listed at a glance.
var fleetStateConfigMapLabels = map[string]string{"rancher-upgrade": "fleet"}

// fleetTargetClusterName returns the cluster name Fleet uses to select the
// target cluster: the Rancher cluster name for provisioned k3s/rke2 clusters
// and the cluster ID otherwise, mirroring the wiring of
// validation/fleet/upgrade.
func fleetTargetClusterName(client *rancher.Client, cluster *extensionClusters.ClusterMeta) (string, error) {
	provisioningClusterID, err := extensionClusters.GetV1ProvisioningClusterByName(client, cluster.Name)
	if err != nil {
		return "", err
	}

	provisioningClusterObject, err := client.Steve.SteveType(extensionClusters.ProvisioningSteveResourceType).ByID(provisioningClusterID)
	if err != nil {
		return "", err
	}

	provisioningCluster := &provv1.Cluster{}
	if err := steveV1.ConvertToK8sType(provisioningClusterObject, provisioningCluster); err != nil {
		return "", err
	}

	kubernetesVersion := provisioningCluster.Spec.KubernetesVersion
	if !strings.Contains(kubernetesVersion, "k3s") && !strings.Contains(kubernetesVersion, "rke2") {
		return cluster.ID, nil
	}

	return client.RancherConfig.ClusterName, nil
}

// cloneFleetExampleRepo clones the public fleet-examples repository into
// fleetGitRepoDir on the SSH node, seeding the remote the GitRepo tracks.
func cloneFleetExampleRepo(sshNode *nodes.Node) error {
	_, err := sshNode.ExecuteCommand(fmt.Sprintf("cd ~/ && git clone %s %s", fleet.ExampleRepo, fleetGitRepoDir))
	return err
}

// createFleetTargetNamespace creates the deterministic fleetTargetNamespace
// on the target cluster via the downstream steve proxy.
func createFleetTargetNamespace(client *rancher.Client, clusterID string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	namespace := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: fleetTargetNamespace},
	}

	_, err = steveClient.SteveType(namespaces.NamespaceSteveType).Create(namespace)
	return err
}

// gitRepoCommit returns the commit SHA currently reported in the GitRepo's
// status.
func gitRepoCommit(client *rancher.Client, gitRepoID string) (string, error) {
	gitRepo, err := client.Steve.SteveType(extensionsfleet.FleetGitRepoResourceType).ByID(gitRepoID)
	if err != nil {
		return "", err
	}

	gitStatus := &fleetv1alpha1.GitRepoStatus{}
	if err := steveV1.ConvertToK8sType(gitRepo.Status, gitStatus); err != nil {
		return "", err
	}

	return gitStatus.Commit, nil
}

// fleetAgentDeploymentID returns the steve deployment ID of the fleet-agent
// on the target cluster. The local cluster's agent runs in
// cattle-fleet-local-system; every other cluster's agent runs in
// cattle-fleet-system.
func fleetAgentDeploymentID(clusterID string) string {
	if clusterID == fleet.LocalName {
		return fleetAgentLocalDeployment
	}

	return fleetAgentDownstreamDeployment
}

// recordFleetState writes the pre-upgrade observations into the deterministic
// ConfigMap in the fleet-default namespace, so the post-upgrade phase, running
// as a separate go test process, can compare against them.
func recordFleetState(client *rancher.Client, data map[string]string) error {
	stateConfigMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      fleetStateConfigMapName,
			Namespace: fleet.Namespace,
			Labels:    fleetStateConfigMapLabels,
		},
		Data: data,
	}

	_, err := client.WranglerContext.Core.ConfigMap().Create(stateConfigMap)
	return err
}

// loadFleetState reads the pre-upgrade observations recorded by the
// pre-upgrade phase.
func loadFleetState(client *rancher.Client) (map[string]string, error) {
	stateConfigMap, err := client.WranglerContext.Core.ConfigMap().Get(fleet.Namespace, fleetStateConfigMapName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	return stateConfigMap.Data, nil
}

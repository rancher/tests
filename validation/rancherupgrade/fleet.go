//go:build (validation || pit.weekly) && !2.8 && !2.9 && !2.10 && !2.11 && !2.12 && !2.13

package rancherupgrade

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os/exec"
	"sort"
	"strings"

	fleetv1alpha1 "github.com/rancher/fleet/pkg/apis/fleet.cattle.io/v1alpha1"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionClusters "github.com/rancher/shepherd/extensions/clusters"
	extensionsfleet "github.com/rancher/shepherd/extensions/fleet"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/namespaces"
	"github.com/rancher/tests/interoperability/fleet"
	"github.com/sirupsen/logrus"
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

// sshNodeConfigKey is the cattle-config section the rancher-upgrade pipeline
// fills for the fleet module: the downstream node SSH material. Downstream
// executors mount no .ssh of their own, so the key cannot be read from disk
// inside the test container.
const sshNodeConfigKey = "sshNode"

// defaultFleetSSHUser matches qa-infra-automation's aws_ssh_user for the
// ansible-provisioned RKE2 nodes when the config omits the user.
const defaultFleetSSHUser = "ubuntu"

// SSHNodeInput holds the downstream node SSH material the rancher-upgrade
// pipeline injects into the test config for the fleet module.
type SSHNodeInput struct {
	SSHKey  string `json:"sshKey" yaml:"sshKey"`
	SSHUser string `json:"sshUser" yaml:"sshUser"`
}

// loadSSHNodeInput returns the sshNode section of the test config.
func loadSSHNodeInput() *SSHNodeInput {
	sshNodeInput := new(SSHNodeInput)
	config.LoadConfig(sshNodeConfigKey, sshNodeInput)

	return sshNodeInput
}

// fleetSSHNode returns the worker node the fleet suite turns into its
// node-local git remote host, keyed with the pipeline-injected SSH material.
// The ansible-provisioned downstream cluster's nodes carry no Rancher machine
// sshkeys link, so ssh.CreateSSHNode cannot resolve a key here. The node is
// picked deterministically (first by name) so the pre-upgrade and
// post-upgrade processes land on the same node.
func fleetSSHNode(client *rancher.Client, clusterID string) (*nodes.Node, error) {
	sshNodeInput := loadSSHNodeInput()
	if sshNodeInput.SSHKey == "" {
		return nil, fmt.Errorf("%s.sshKey is missing from the test config: the rancher-upgrade pipeline injects the node key for the fleet module", sshNodeConfigKey)
	}

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, err
	}

	query, err := url.ParseQuery(clusters.LabelWorker)
	if err != nil {
		return nil, err
	}

	nodeList, err := steveClient.SteveType("node").List(query)
	if err != nil {
		return nil, err
	}
	if len(nodeList.Data) == 0 {
		return nil, errors.New("node list is empty")
	}

	sort.Slice(nodeList.Data, func(i, j int) bool {
		return nodeList.Data[i].Name < nodeList.Data[j].Name
	})
	firstNode := nodeList.Data[0]

	node := &corev1.Node{}
	if err := steveV1.ConvertToK8sType(firstNode.JSONResp, node); err != nil {
		return nil, err
	}

	var externalIP, internalIP string
	for _, address := range node.Status.Addresses {
		switch address.Type {
		case corev1.NodeExternalIP:
			if externalIP == "" {
				externalIP = address.Address
			}
		case corev1.NodeInternalIP:
			if internalIP == "" {
				internalIP = address.Address
			}
		}
	}

	nodeIP, err := fleetPublicIP(externalIP, internalIP)
	if err != nil {
		return nil, err
	}

	sshUser := sshNodeInput.SSHUser
	if sshUser == "" {
		sshUser = defaultFleetSSHUser
	}

	logrus.Infof("fleet module: using node %s at %s (user %s) as the git remote host", node.Name, nodeIP, sshUser)

	return &nodes.Node{
		NodeID:          firstNode.ID,
		PublicIPAddress: nodeIP,
		SSHUser:         sshUser,
		SSHKey:          []byte(sshNodeInput.SSHKey),
	}, nil
}

// awsNodeTemplateConfigKey mirrors the pipeline's awsNodeTemplate config
// section, which carries the deployment region.
const awsNodeTemplateConfigKey = "awsNodeTemplate"

// defaultFleetAWSRegion matches the pipeline's default deployment region.
const defaultFleetAWSRegion = "us-east-2"

// awsNodeTemplateInput carries the awsNodeTemplate config fields the fleet
// module needs.
type awsNodeTemplateInput struct {
	Region string `json:"region" yaml:"region"`
}

// fleetPublicIP returns the address the suite can SSH to: the node's external
// address when kubelet reports one, otherwise the public IP resolved from the
// instance's internal VPC address. The ansible-provisioned instances get
// public IPs from terraform, but kubelet does not report them as node
// addresses; the aws CLI (present in the go-test image, authenticated through
// the executor's AWS credentials) performs the mapping.
func fleetPublicIP(externalIP, internalIP string) (string, error) {
	if externalIP != "" {
		return externalIP, nil
	}
	if internalIP == "" {
		return "", errors.New("node reports neither an external nor an internal address")
	}

	regionInput := new(awsNodeTemplateInput)
	config.LoadConfig(awsNodeTemplateConfigKey, regionInput)
	region := regionInput.Region
	if region == "" {
		region = defaultFleetAWSRegion
	}

	out, err := exec.Command("aws", "ec2", "describe-instances",
		"--region", region,
		"--filters", "Name=private-ip-address,Values="+internalIP,
		"--query", "Reservations[].Instances[].PublicIpAddress",
		"--output", "json").Output()
	if err != nil {
		return "", fmt.Errorf("resolving the public IP for internal address %s via aws ec2: %w", internalIP, err)
	}

	var resolved [][]string
	if err := json.Unmarshal(out, &resolved); err != nil {
		return "", fmt.Errorf("parsing the aws ec2 describe-instances output: %w", err)
	}
	if len(resolved) == 0 || len(resolved[0]) == 0 || resolved[0][0] == "" {
		return "", fmt.Errorf("no public IP found for internal address %s in %s", internalIP, region)
	}

	return resolved[0][0], nil
}

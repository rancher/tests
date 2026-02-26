// Package qainfraautomation provides high-level orchestration functions that
// combine OpenTofu infrastructure provisioning with Ansible cluster configuration
// using the rancher-qa-infra-automation repository.
package qainfraautomation

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/interoperability/qainfraautomation/ansible"
	"github.com/rancher/tests/interoperability/qainfraautomation/config"
	"github.com/rancher/tests/interoperability/qainfraautomation/tofu"
	"github.com/sirupsen/logrus"
)

const (
	// harvesterVMModulePath is the path within the qa-infra-automation repo to the Harvester VM module.
	harvesterVMModulePath = "tofu/harvester/modules/vm"

	// harvesterKubeconfigDest is the hardcoded kubeconfig path expected by the Harvester provider HCL.
	harvesterKubeconfigDest = "tofu/harvester/modules/local.yaml"

	// rancherCustomClusterModulePath is the path within the qa-infra-automation repo to the Rancher custom cluster module.
	rancherCustomClusterModulePath = "tofu/rancher/custom_cluster"

	// rancherClusterModulePath is the path within the qa-infra-automation repo to the Rancher-provisioned cluster module.
	rancherClusterModulePath = "tofu/rancher/cluster"

	// customClusterPlaybook is the Ansible playbook path (relative to repo root) for registering nodes to a Rancher custom cluster.
	customClusterPlaybook = "ansible/rancher/downstream/custom_cluster/custom-cluster-playbook.yml"

	// customClusterInventoryTemplate is the template path for the custom cluster Ansible inventory.
	customClusterInventoryTemplate = "ansible/rancher/downstream/custom_cluster/inventory-template.yml"

	// customClusterInventoryOutput is the rendered inventory path used at runtime.
	customClusterInventoryOutput = "ansible/rancher/downstream/custom_cluster/inventory.yml"

	// rke2Playbook is the Ansible playbook path for standalone RKE2 cluster creation.
	rke2Playbook = "ansible/rke2/default/rke2-playbook.yml"

	// rke2InventoryTemplate is the template path for the RKE2 Ansible inventory.
	rke2InventoryTemplate = "ansible/rke2/default/inventory-template.yml"

	// rke2InventoryOutput is the rendered inventory path for RKE2.
	rke2InventoryOutput = "ansible/rke2/default/inventory.yml"

	// rke2VarsFile is the vars.yaml path for the RKE2 playbook (relative to repo root).
	rke2VarsFile = "ansible/rke2/default/vars.yaml"

	// k3sPlaybook is the Ansible playbook path for standalone K3S cluster creation.
	k3sPlaybook = "ansible/k3s/default/k3s-playbook.yml"

	// k3sInventoryTemplate is the template path for the K3S Ansible inventory.
	k3sInventoryTemplate = "ansible/k3s/default/inventory-template.yml"

	// k3sInventoryOutput is the rendered inventory path for K3S.
	k3sInventoryOutput = "ansible/k3s/default/inventory.yml"

	// k3sVarsFile is the vars.yaml path for the K3S playbook (relative to repo root).
	k3sVarsFile = "ansible/k3s/default/vars.yaml"

	// fleetDefaultNamespace is the Rancher namespace where custom downstream clusters are registered.
	fleetDefaultNamespace = "fleet-default"
)

// harvesterVMVars represents the JSON tfvars structure expected by the Harvester VM tofu module.
type harvesterVMVars struct {
	SSHKey       string              `json:"ssh_key"`
	Nodes        []harvesterNodeSpec `json:"nodes"`
	GenerateName string              `json:"generate_name,omitempty"`
	SSHUser      string              `json:"ssh_user,omitempty"`
	NetworkName  string              `json:"network_name,omitempty"`
	ImageID      string              `json:"image_id,omitempty"`
	Namespace    string              `json:"namespace,omitempty"`
	CPU          int                 `json:"cpu,omitempty"`
	Memory       string              `json:"mem,omitempty"`
	DiskSize     string              `json:"disk_size,omitempty"`
}

type harvesterNodeSpec struct {
	Count int      `json:"count"`
	Role  []string `json:"role"`
}

// rancherCustomClusterVars represents the JSON tfvars structure for the rancher/custom_cluster tofu module.
type rancherCustomClusterVars struct {
	KubernetesVersion string `json:"kubernetes_version"`
	FQDN              string `json:"fqdn"`
	APIKey            string `json:"api_key"`
	GenerateName      string `json:"generate_name,omitempty"`
	IsNetworkPolicy   bool   `json:"is_network_policy,omitempty"`
	PSA               string `json:"psa,omitempty"`
	Insecure          bool   `json:"insecure"`
}

// rancherClusterMachinePool mirrors the machine_pools object accepted by the tofu/rancher/cluster module.
type rancherClusterMachinePool struct {
	Name             string `json:"name,omitempty"`
	ControlPlaneRole bool   `json:"control_plane_role,omitempty"`
	WorkerRole       bool   `json:"worker_role,omitempty"`
	EtcdRole         bool   `json:"etcd_role,omitempty"`
	Quantity         int    `json:"quantity,omitempty"`
}

// rancherClusterVars represents the JSON tfvars structure for the tofu/rancher/cluster module.
type rancherClusterVars struct {
	KubernetesVersion string                      `json:"kubernetes_version"`
	FQDN              string                      `json:"fqdn"`
	APIKey            string                      `json:"api_key"`
	Insecure          bool                        `json:"insecure"`
	CloudProvider     string                      `json:"cloud_provider"`
	GenerateName      string                      `json:"generate_name,omitempty"`
	IsNetworkPolicy   bool                        `json:"is_network_policy,omitempty"`
	PSA               string                      `json:"psa,omitempty"`
	MachinePools      []rancherClusterMachinePool `json:"machine_pools,omitempty"`
	NodeConfig        map[string]interface{}      `json:"node_config"`
	CreateNew         bool                        `json:"create_new"`
}

// ProvisionRancherCluster provisions a downstream cluster in Rancher where Rancher itself manages
// node provisioning via a cloud provider node driver (e.g. Harvester, AWS, Linode).
// It uses the tofu/rancher/cluster module from the rancher-qa-infra-automation repository.
//
// Cleanup (tofu destroy + Rancher API delete) is registered automatically via t.Cleanup().
//
// Parameters:
//   - t: the test handle used to register cleanup and fail the test on error.
//   - rancherClient: an authenticated Rancher shepherd client.
//   - cfg: the top-level QA infra automation config (from the "qaInfraAutomation" config key).
//   - clusterCfg: parameters for the Rancher-provisioned cluster (cloud provider, node config, k8s version, etc.).
func ProvisionRancherCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.RancherClusterConfig,
) *v1.SteveAPIObject {
	t.Helper()

	repoPath := cfg.RepoPath
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	generateName := clusterCfg.GenerateName
	if generateName == "" {
		generateName = "tf"
	}

	// Build machine_pools, defaulting to a single all-roles pool if none specified.
	machinePools := make([]rancherClusterMachinePool, len(clusterCfg.MachinePools))
	for i, mp := range clusterCfg.MachinePools {
		quantity := mp.Quantity
		if quantity == 0 {
			quantity = 1
		}
		machinePools[i] = rancherClusterMachinePool{
			Name:             fmt.Sprintf("%s-%d", generateName, i),
			ControlPlaneRole: mp.ControlPlaneRole,
			WorkerRole:       mp.WorkerRole,
			EtcdRole:         mp.EtcdRole,
			Quantity:         quantity,
		}
	}
	if len(machinePools) == 0 {
		machinePools = []rancherClusterMachinePool{
			{Name: generateName, ControlPlaneRole: true, WorkerRole: true, EtcdRole: true, Quantity: 1},
		}
	}

	clusterVars := rancherClusterVars{
		KubernetesVersion: clusterCfg.KubernetesVersion,
		FQDN:              "https://" + rancherClient.RancherConfig.Host,
		APIKey:            rancherClient.RancherConfig.AdminToken,
		Insecure:          true,
		CloudProvider:     clusterCfg.CloudProvider,
		GenerateName:      generateName,
		IsNetworkPolicy:   clusterCfg.IsNetworkPolicy,
		PSA:               clusterCfg.PSA,
		MachinePools:      machinePools,
		NodeConfig:        clusterCfg.NodeConfig,
		CreateNew:         true,
	}

	clusterVarFile, err := writeTFVarsJSON(repoPath, "rancher-cluster-vars.json", clusterVars)
	if err != nil {
		t.Fatalf("write rancher cluster tfvars: %v", err)
	}

	clusterModuleDir := filepath.Join(repoPath, rancherClusterModulePath)
	clusterTofu := tofu.NewClient(clusterModuleDir, workspace)

	if err := clusterTofu.Init(); err != nil {
		t.Fatalf("tofu init (rancher cluster): %v", err)
	}
	if err := clusterTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (rancher cluster): %v", err)
	}
	if err := clusterTofu.Apply(clusterVarFile); err != nil {
		t.Fatalf("tofu apply (rancher cluster): %v", err)
	}

	// Read the cluster name from the tofu output.
	clusterName, err := clusterTofu.Output("name")
	if err != nil {
		t.Fatalf("tofu output name: %v", err)
	}
	logrus.Infof("[qainfraautomation] rancher-provisioned cluster name from tofu: %s", clusterName)

	// Register cleanup before verifying readiness so infrastructure is always
	// torn down even if the cluster never reaches a ready state.
	t.Cleanup(func() {
		logrus.Infof("[qainfraautomation] destroying Rancher-provisioned cluster %q (workspace=%s)", clusterName, workspace)
		if err := clusterTofu.Destroy(clusterVarFile); err != nil {
			logrus.Warnf("[qainfraautomation] tofu destroy: %v", err)
		}

		// Ensure the cluster is gone from Rancher. tofu destroy may not always
		// remove the cluster object if the provider state is inconsistent.
		existing, err := rancherClient.Steve.SteveType(stevetypes.Provisioning).ByID(fleetDefaultNamespace + "/" + clusterName)
		if err != nil {
			if strings.Contains(err.Error(), "404 Not Found") {
				return
			}
			logrus.Errorf("[qainfraautomation] checking cluster %q after destroy: %v", clusterName, err)
			return
		}

		logrus.Infof("[qainfraautomation] cluster %q still present after tofu destroy; deleting via Rancher API", clusterName)
		if err := rancherClient.Steve.SteveType(stevetypes.Provisioning).Delete(existing); err != nil {
			logrus.Errorf("[qainfraautomation] delete cluster %q from Rancher: %v", clusterName, err)
		}
	})

	// Fetch the cluster object from Rancher and verify it is ready.
	clusterObj, err := rancherClient.Steve.SteveType(stevetypes.Provisioning).ByID(fleetDefaultNamespace + "/" + clusterName)
	if err != nil {
		t.Fatalf("fetch cluster %q from Rancher: %v", clusterName, err)
	}

	if err := provisioning.VerifyClusterReady(rancherClient, clusterObj); err != nil {
		t.Fatalf("cluster %q did not become ready: %v", clusterName, err)
	}

	return clusterObj
}

// ProvisionHarvesterCustomCluster provisions Harvester VMs via OpenTofu, creates a Rancher custom
// downstream cluster via OpenTofu + Ansible, and returns a *v1.SteveAPIObject for the cluster.
//
// Cleanup (tofu destroy for both modules, Rancher cluster first then VMs) is registered
// automatically via t.Cleanup().
//
// Parameters:
//   - t: the test handle used to register cleanup and fail the test on error.
//   - rancherClient: an authenticated Rancher shepherd client.
//   - cfg: the top-level QA infra automation config (from the "qaInfraAutomation" config key).
//   - clusterCfg: parameters for the Rancher custom cluster (kubernetes version, name prefix, etc.).
func ProvisionHarvesterCustomCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.CustomClusterConfig,
) *v1.SteveAPIObject {
	t.Helper()

	repoPath := cfg.RepoPath
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	h := cfg.Harvester
	if h == nil {
		t.Fatalf("harvester config is required for ProvisionHarvesterCustomCluster")
	}

	// -------------------------------------------------------------------------
	// Step 1: Copy Harvester kubeconfig to the hardcoded location expected by HCL.
	// -------------------------------------------------------------------------
	destKubeconfig := filepath.Join(repoPath, harvesterKubeconfigDest)
	logrus.Infof("[qainfraautomation] copying Harvester kubeconfig %s → %s", h.KubeConfigPath, destKubeconfig)
	if err := copyFile(h.KubeConfigPath, destKubeconfig); err != nil {
		t.Fatalf("copy harvester kubeconfig: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 2: Write Harvester VM tfvars.json.
	// -------------------------------------------------------------------------
	vmVars := buildHarvesterVMVars(h)
	vmVarFile, err := writeTFVarsJSON(repoPath, "harvester-vm-vars.json", vmVars)
	if err != nil {
		t.Fatalf("write harvester VM tfvars: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 3: Tofu init + workspace + apply for Harvester VM module.
	// -------------------------------------------------------------------------
	vmModuleDir := filepath.Join(repoPath, harvesterVMModulePath)
	vmTofu := tofu.NewClient(vmModuleDir, workspace)

	if err := vmTofu.Init(); err != nil {
		t.Fatalf("tofu init (harvester vm): %v", err)
	}
	if err := vmTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (harvester vm): %v", err)
	}
	if err := vmTofu.Apply(vmVarFile); err != nil {
		t.Fatalf("tofu apply (harvester vm): %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 4: Build Rancher custom cluster tfvars and apply.
	// -------------------------------------------------------------------------
	clusterVars := rancherCustomClusterVars{
		KubernetesVersion: clusterCfg.KubernetesVersion,
		FQDN:              "https://" + rancherClient.RancherConfig.Host,
		APIKey:            rancherClient.RancherConfig.AdminToken,
		GenerateName:      clusterCfg.GenerateName,
		IsNetworkPolicy:   clusterCfg.IsNetworkPolicy,
		PSA:               clusterCfg.PSA,
		Insecure:          true,
	}
	if clusterVars.GenerateName == "" {
		clusterVars.GenerateName = "tf"
	}

	clusterVarFile, err := writeTFVarsJSON(repoPath, "rancher-custom-cluster-vars.json", clusterVars)
	if err != nil {
		t.Fatalf("write rancher custom cluster tfvars: %v", err)
	}

	clusterModuleDir := filepath.Join(repoPath, rancherCustomClusterModulePath)
	clusterTofu := tofu.NewClient(clusterModuleDir, workspace)

	if err := clusterTofu.Init(); err != nil {
		t.Fatalf("tofu init (rancher custom cluster): %v", err)
	}
	if err := clusterTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (rancher custom cluster): %v", err)
	}
	if err := clusterTofu.Apply(clusterVarFile); err != nil {
		t.Fatalf("tofu apply (rancher custom cluster): %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 5: Generate Ansible inventory from template.
	// -------------------------------------------------------------------------
	ansibleClient := ansible.NewClient(repoPath)

	inventoryEnv := map[string]string{
		"TERRAFORM_NODE_SOURCE": harvesterVMModulePath,
		"TF_WORKSPACE":          workspace,
	}
	if err := ansibleClient.GenerateInventory(customClusterInventoryTemplate, customClusterInventoryOutput, inventoryEnv); err != nil {
		t.Fatalf("generate inventory: %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 6: Add SSH key to agent and run the custom cluster playbook.
	// -------------------------------------------------------------------------
	if err := ansibleClient.AddSSHKey(h.SSHPrivateKeyPath); err != nil {
		t.Fatalf("ssh-add: %v", err)
	}

	playbookEnv := []string{
		"TF_WORKSPACE=" + workspace,
		"TERRAFORM_NODE_SOURCE=" + harvesterVMModulePath,
	}
	if err := ansibleClient.RunPlaybook(customClusterPlaybook, customClusterInventoryOutput, playbookEnv); err != nil {
		t.Fatalf("ansible-playbook (custom cluster): %v", err)
	}

	// -------------------------------------------------------------------------
	// Step 7: Read the cluster_name output from the rancher custom_cluster module.
	// -------------------------------------------------------------------------
	clusterName, err := clusterTofu.Output("cluster_name")
	if err != nil {
		t.Fatalf("tofu output cluster_name: %v", err)
	}
	logrus.Infof("[qainfraautomation] cluster name from tofu: %s", clusterName)

	// Register cleanup before verifying readiness so infrastructure is always
	// torn down even if the cluster never reaches a ready state.
	// Destroy Rancher cluster resources first, then VMs (LIFO order via two separate t.Cleanup calls).
	t.Cleanup(func() {
		logrus.Infof("[qainfraautomation] destroying Harvester VMs (workspace=%s)", workspace)
		if err := vmTofu.Destroy(vmVarFile); err != nil {
			logrus.Errorf("[qainfraautomation] tofu destroy (harvester vm): %v", err)
		}
	})
	t.Cleanup(func() {
		logrus.Infof("[qainfraautomation] destroying Rancher custom cluster resources (workspace=%s)", workspace)
		if err := clusterTofu.Destroy(clusterVarFile); err != nil {
			logrus.Errorf("[qainfraautomation] tofu destroy (rancher custom cluster): %v", err)
		}
	})

	// -------------------------------------------------------------------------
	// Step 8: Fetch the cluster object from Rancher and verify it is ready.
	// -------------------------------------------------------------------------
	clusterObj, err := rancherClient.Steve.SteveType(stevetypes.Provisioning).ByID(fleetDefaultNamespace + "/" + clusterName)
	if err != nil {
		t.Fatalf("fetch cluster %q from Rancher: %v", clusterName, err)
	}

	if err := provisioning.VerifyClusterReady(rancherClient, clusterObj); err != nil {
		t.Fatalf("cluster %q did not become ready: %v", clusterName, err)
	}

	return clusterObj
}

// ProvisionHarvesterRKE2Cluster provisions Harvester VMs via OpenTofu and then installs a standalone
// RKE2 cluster on them via Ansible. It returns the path to the kubeconfig file for the new cluster.
//
// Cleanup (tofu destroy for the VM module) is registered automatically via t.Cleanup().
func ProvisionHarvesterRKE2Cluster(
	t *testing.T,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
) string {
	t.Helper()
	return provisionHarvesterStandaloneCluster(t, cfg, clusterCfg, "rke2")
}

// ProvisionHarvesterK3SCluster provisions Harvester VMs via OpenTofu and then installs a standalone
// K3S cluster on them via Ansible. It returns the path to the kubeconfig file for the new cluster.
//
// Cleanup (tofu destroy for the VM module) is registered automatically via t.Cleanup().
func ProvisionHarvesterK3SCluster(
	t *testing.T,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
) string {
	t.Helper()
	return provisionHarvesterStandaloneCluster(t, cfg, clusterCfg, "k3s")
}

// provisionHarvesterStandaloneCluster is the shared implementation for RKE2 and K3S standalone clusters.
func provisionHarvesterStandaloneCluster(
	t *testing.T,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
	clusterType string, // "rke2" or "k3s"
) string {
	t.Helper()

	repoPath := cfg.RepoPath
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	h := cfg.Harvester
	if h == nil {
		t.Fatalf("harvester config is required for standalone cluster provisioning")
	}

	// Select playbook/inventory/vars paths based on cluster type.
	var playbookPath, inventoryTemplate, inventoryOutput, varsFile string
	switch clusterType {
	case "rke2":
		playbookPath = rke2Playbook
		inventoryTemplate = rke2InventoryTemplate
		inventoryOutput = rke2InventoryOutput
		varsFile = rke2VarsFile
	case "k3s":
		playbookPath = k3sPlaybook
		inventoryTemplate = k3sInventoryTemplate
		inventoryOutput = k3sInventoryOutput
		varsFile = k3sVarsFile
	default:
		t.Fatalf("unsupported cluster type: %s (must be rke2 or k3s)", clusterType)
	}

	// Step 1: Copy Harvester kubeconfig.
	destKubeconfig := filepath.Join(repoPath, harvesterKubeconfigDest)
	logrus.Infof("[qainfraautomation] copying Harvester kubeconfig %s → %s", h.KubeConfigPath, destKubeconfig)
	if err := copyFile(h.KubeConfigPath, destKubeconfig); err != nil {
		t.Fatalf("copy harvester kubeconfig: %v", err)
	}

	// Step 2: Write Harvester VM tfvars.json.
	vmVars := buildHarvesterVMVars(h)
	vmVarFile, err := writeTFVarsJSON(repoPath, "harvester-vm-vars.json", vmVars)
	if err != nil {
		t.Fatalf("write harvester VM tfvars: %v", err)
	}

	// Step 3: Tofu init + workspace + apply for Harvester VM module.
	vmModuleDir := filepath.Join(repoPath, harvesterVMModulePath)
	vmTofu := tofu.NewClient(vmModuleDir, workspace)

	if err := vmTofu.Init(); err != nil {
		t.Fatalf("tofu init (harvester vm): %v", err)
	}
	if err := vmTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (harvester vm): %v", err)
	}
	if err := vmTofu.Apply(vmVarFile); err != nil {
		t.Fatalf("tofu apply (harvester vm): %v", err)
	}

	// Register cleanup after VMs are applied so they're always destroyed.
	t.Cleanup(func() {
		logrus.Infof("[qainfraautomation] destroying Harvester VMs (workspace=%s)", workspace)
		if err := vmTofu.Destroy(vmVarFile); err != nil {
			logrus.Errorf("[qainfraautomation] tofu destroy (harvester vm): %v", err)
		}
	})

	// Step 4: Generate inventory.
	ansibleClient := ansible.NewClient(repoPath)
	inventoryEnv := map[string]string{
		"TERRAFORM_NODE_SOURCE": harvesterVMModulePath,
		"TF_WORKSPACE":          workspace,
	}
	if err := ansibleClient.GenerateInventory(inventoryTemplate, inventoryOutput, inventoryEnv); err != nil {
		t.Fatalf("generate inventory: %v", err)
	}

	// Step 5: Write vars.yaml for the playbook.
	vars := map[string]string{
		"kubernetes_version": clusterCfg.KubernetesVersion,
		"cni":                clusterCfg.CNI,
		"channel":            clusterCfg.Channel,
		"kubeconfig_file":    clusterCfg.KubeconfigOutputPath,
	}
	if err := ansibleClient.WriteVarsYAML(varsFile, vars); err != nil {
		t.Fatalf("write vars.yaml: %v", err)
	}

	// Step 6: Add SSH key + run playbook.
	if err := ansibleClient.AddSSHKey(h.SSHPrivateKeyPath); err != nil {
		t.Fatalf("ssh-add: %v", err)
	}

	playbookEnv := []string{
		"TF_WORKSPACE=" + workspace,
		"TERRAFORM_NODE_SOURCE=" + harvesterVMModulePath,
	}
	if err := ansibleClient.RunPlaybook(playbookPath, inventoryOutput, playbookEnv); err != nil {
		t.Fatalf("ansible-playbook (%s): %v", clusterType, err)
	}

	return clusterCfg.KubeconfigOutputPath
}

// -------------------------------------------------------------------------
// Internal helpers
// -------------------------------------------------------------------------

// buildHarvesterVMVars constructs the tofu var struct from a HarvesterConfig.
func buildHarvesterVMVars(h *config.HarvesterConfig) harvesterVMVars {
	nodes := make([]harvesterNodeSpec, len(h.Nodes))
	for i, n := range h.Nodes {
		nodes[i] = harvesterNodeSpec{
			Count: n.Count,
			Role:  n.Role,
		}
	}
	return harvesterVMVars{
		SSHKey:       h.SSHPublicKey,
		Nodes:        nodes,
		GenerateName: h.GenerateName,
		SSHUser:      h.SSHUser,
		NetworkName:  h.NetworkName,
		ImageID:      h.ImageID,
		Namespace:    h.Namespace,
		CPU:          h.CPU,
		Memory:       h.Memory,
		DiskSize:     h.DiskSize,
	}
}

// writeTFVarsJSON marshals v as JSON and writes it to <repoPath>/<filename>.
// Returns the absolute path to the written file.
func writeTFVarsJSON(repoPath, filename string, v any) (string, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal tfvars JSON: %w", err)
	}
	destPath := filepath.Join(repoPath, filename)
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return "", fmt.Errorf("write tfvars file %s: %w", destPath, err)
	}
	logrus.Debugf("[qainfraautomation] wrote tfvars: %s", destPath)
	return destPath, nil
}

// copyFile copies the file at src to dst, creating or overwriting dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(dst), err)
	}
	return os.WriteFile(dst, data, 0600)
}

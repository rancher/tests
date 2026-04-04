package qainfraautomation

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/crypto/ssh"

	infraAnsible "github.com/rancher/qa-infra-automation/ansible"
	"github.com/rancher/qa-infra-automation/fsutil"
	infraTofu "github.com/rancher/qa-infra-automation/tofu"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	shepnodes "github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/tests/interoperability/qainfraautomation/ansible"
	"github.com/rancher/tests/interoperability/qainfraautomation/config"
	"github.com/rancher/tests/interoperability/qainfraautomation/tofu"
	"github.com/sirupsen/logrus"
	"gopkg.in/yaml.v2"
)

func cleanupEnabled(rancherClient *rancher.Client) bool {
	c := rancherClient.RancherConfig.Cleanup
	return c == nil || *c
}

// infraCleanupEnabled returns whether infrastructure cleanup should run after the test.
func infraCleanupEnabled(cfg *config.Config) bool {
	if cfg.RancherInstall != nil && cfg.RancherInstall.Cleanup != nil {
		return *cfg.RancherInstall.Cleanup
	}
	return true
}

const (
	harvesterVMModulePath          = "tofu/harvester/modules/vm"
	harvesterKubeconfigDest        = "tofu/harvester/modules/local.yaml"
	rancherCustomClusterModulePath = "tofu/rancher/custom_cluster"
	rancherClusterModulePath       = "tofu/rancher/cluster"
	customClusterPlaybook          = "ansible/rancher/downstream/custom_cluster/custom-cluster-playbook.yml"
	customClusterInventoryTemplate = "ansible/rancher/downstream/custom_cluster/inventory-template.yml"
	rke2Playbook                   = "ansible/rke2/default/rke2-playbook.yml"
	rke2InventoryTemplate          = "ansible/rke2/default/inventory-template.yml"
	rke2VarsFile                   = "ansible/rke2/default/vars.yaml"
	k3sPlaybook                    = "ansible/k3s/default/k3s-playbook.yml"
	k3sInventoryTemplate           = "ansible/k3s/default/inventory-template.yml"
	k3sVarsFile                    = "ansible/k3s/default/vars.yaml"
	awsClusterNodesModulePath      = "tofu/aws/modules/cluster_nodes"
	fleetDefaultNamespace          = "fleet-default"
	rancherInstallPlaybook         = "ansible/rancher/default-ha/rancher-playbook.yml"
	rancherInstallVarsFile         = "ansible/rancher/default-ha/vars.yaml"
)

// extractInfraFiles extracts the embedded Ansible and Tofu files into a temporary
// directory on disk. When keepOnDisk is true, the directory is not removed after
// the test finishes (preserving Tofu state for a later manual destroy).
func extractInfraFiles(t *testing.T, keepOnDisk bool) string {
	t.Helper()

	dir, err := os.MkdirTemp("", "qa-infra-*")
	if err != nil {
		t.Fatalf("create temp dir for infra files: %v", err)
	}
	if keepOnDisk {
		logrus.Warnf("[qainfraautomation] cleanup disabled: temp infra dir will be preserved at %s", dir)
	} else {
		t.Cleanup(func() {
			if err := os.RemoveAll(dir); err != nil {
				logrus.Warnf("[qainfraautomation] failed to remove temp infra dir %s: %v", dir, err)
			}
		})
	}

	ansibleDir := filepath.Join(dir, "ansible")
	if err := os.MkdirAll(ansibleDir, 0755); err != nil {
		t.Fatalf("mkdir ansible: %v", err)
	}
	if err := fsutil.WriteToDisk(infraAnsible.Files, ansibleDir); err != nil {
		t.Fatalf("extract embedded ansible files: %v", err)
	}

	tofuDir := filepath.Join(dir, "tofu")
	if err := os.MkdirAll(tofuDir, 0755); err != nil {
		t.Fatalf("mkdir tofu: %v", err)
	}
	if err := fsutil.WriteToDisk(infraTofu.Files, tofuDir); err != nil {
		t.Fatalf("extract embedded tofu files: %v", err)
	}

	logrus.Infof("[qainfraautomation] extracted embedded infra files to %s", dir)
	return dir
}

type harvesterVMVars struct {
	SSHKey             string              `json:"ssh_key"`
	Nodes              []harvesterNodeSpec `json:"nodes"`
	GenerateName       string              `json:"generate_name,omitempty"`
	SSHUser            string              `json:"ssh_user,omitempty"`
	NetworkName        string              `json:"network_name,omitempty"`
	BackendNetworkName string              `json:"backend_network_name,omitempty"`
	ImageID            string              `json:"image_id,omitempty"`
	Namespace          string              `json:"namespace,omitempty"`
	CPU                int                 `json:"cpu,omitempty"`
	Memory             string              `json:"mem,omitempty"`
	DiskSize           string              `json:"disk_size,omitempty"`
	CreateLoadbalancer bool                `json:"create_loadbalancer"`
	SubnetCIDR         string              `json:"subnet_cidr,omitempty"`
	GatewayIP          string              `json:"gateway_ip,omitempty"`
	RangeIPStart       string              `json:"range_ip_start,omitempty"`
	RangeIPEnd         string              `json:"range_ip_end,omitempty"`
	IPPoolName         string              `json:"ippool_name,omitempty"`
}

type harvesterNodeSpec struct {
	Count int      `json:"count"`
	Role  []string `json:"role"`
}

type rancherCustomClusterVars struct {
	KubernetesVersion string `json:"kubernetes_version"`
	FQDN              string `json:"fqdn"`
	APIKey            string `json:"api_key"`
	GenerateName      string `json:"generate_name,omitempty"`
	IsNetworkPolicy   bool   `json:"is_network_policy,omitempty"`
	PSA               string `json:"psa,omitempty"`
	Insecure          bool   `json:"insecure"`
}

type rancherClusterMachinePool struct {
	Name             string `json:"name,omitempty"`
	ControlPlaneRole bool   `json:"control_plane_role,omitempty"`
	WorkerRole       bool   `json:"worker_role,omitempty"`
	EtcdRole         bool   `json:"etcd_role,omitempty"`
	Quantity         int    `json:"quantity,omitempty"`
}

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

// ProvisionRancherCluster provisions a Rancher-managed cluster via OpenTofu and waits for it to be ready.
func ProvisionRancherCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.RancherClusterConfig,
) *v1.SteveAPIObject {
	t.Helper()

	repoPath := extractInfraFiles(t, false)
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	generateName := clusterCfg.GenerateName
	if generateName == "" {
		generateName = "tf"
	}

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
		Insecure:          *rancherClient.RancherConfig.Insecure,
		CloudProvider:     clusterCfg.CloudProvider,
		GenerateName:      generateName,
		IsNetworkPolicy:   clusterCfg.IsNetworkPolicy,
		PSA:               clusterCfg.PSA,
		MachinePools:      machinePools,
		NodeConfig:        clusterCfg.NodeConfig,
		CreateNew:         true,
	}

	clusterVarFile, err := writeTFVarsJSON(t, "rancher-cluster-vars.json", clusterVars)
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

	clusterName, err := clusterTofu.Output("name")
	if err != nil {
		t.Fatalf("tofu output name: %v", err)
	}
	logrus.Infof("[qainfraautomation] rancher-provisioned cluster name from tofu: %s", clusterName)

	if cleanupEnabled(rancherClient) {
		t.Cleanup(func() {
			logrus.Infof("[qainfraautomation] destroying Rancher-provisioned cluster %q (workspace=%s)", clusterName, workspace)
			if err := clusterTofu.Destroy(clusterVarFile); err != nil {
				logrus.Warnf("[qainfraautomation] tofu destroy: %v", err)
			}

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
	}

	clusterObj, err := rancherClient.Steve.SteveType(stevetypes.Provisioning).ByID(fleetDefaultNamespace + "/" + clusterName)
	if err != nil {
		t.Fatalf("fetch cluster %q from Rancher after ready: %v", clusterName, err)
	}

	return clusterObj
}

// ProvisionCustomCluster provisions a custom (node-driver) Rancher cluster on either AWS or Harvester infrastructure.
func ProvisionCustomCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.CustomClusterConfig,
) *v1.SteveAPIObject {
	t.Helper()

	repoPath := extractInfraFiles(t, false)

	switch {
	case cfg.AWS != nil && cfg.Harvester != nil:
		t.Fatalf("ProvisionCustomCluster: both aws and harvester are set in config; exactly one must be provided")
		return nil
	case cfg.AWS != nil:
		logrus.Infof("[qainfraautomation] ProvisionCustomCluster: provisioning via AWS provider (workspace=%s)", cfg.Workspace)
		return provisionAWSCustomCluster(t, rancherClient, cfg, clusterCfg, repoPath)
	case cfg.Harvester != nil:
		logrus.Infof("[qainfraautomation] ProvisionCustomCluster: provisioning via Harvester provider (workspace=%s)", cfg.Workspace)
		return provisionHarvesterCustomCluster(t, rancherClient, cfg, clusterCfg, repoPath)
	default:
		t.Fatalf("ProvisionCustomCluster: neither aws nor harvester config is set; exactly one must be provided")
		return nil
	}
}

type awsNodeSpec struct {
	Count int      `json:"count"`
	Role  []string `json:"role"`
}

type awsClusterNodesVars struct {
	PublicSSHKey   string        `json:"public_ssh_key"`
	AccessKey      string        `json:"aws_access_key"`
	SecretKey      string        `json:"aws_secret_key"`
	Region         string        `json:"aws_region"`
	AMI            string        `json:"aws_ami"`
	HostnamePrefix string        `json:"aws_hostname_prefix"`
	Route53Zone    string        `json:"aws_route53_zone"`
	SSHUser        string        `json:"aws_ssh_user"`
	SecurityGroup  []string      `json:"aws_security_group"`
	VPC            string        `json:"aws_vpc"`
	VolumeSize     int           `json:"aws_volume_size"`
	VolumeType     string        `json:"aws_volume_type"`
	Subnet         string        `json:"aws_subnet"`
	InstanceType   string        `json:"instance_type"`
	Nodes          []awsNodeSpec `json:"nodes"`
	AirgapSetup    bool          `json:"airgap_setup"`
	ProxySetup     bool          `json:"proxy_setup"`
}

func provisionAWSCustomCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.CustomClusterConfig,
	repoPath string,
) *v1.SteveAPIObject {
	t.Helper()

	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	a := cfg.AWS

	hostnamePrefix := a.HostnamePrefix
	if hostnamePrefix == "" {
		hostnamePrefix = "tf"
	}

	volumeSize := a.VolumeSize
	if volumeSize == 0 {
		volumeSize = 50
	}

	volumeType := a.VolumeType
	if volumeType == "" {
		volumeType = "gp3"
	}

	nodes := make([]awsNodeSpec, len(clusterCfg.Nodes))
	for i, n := range clusterCfg.Nodes {
		nodes[i] = awsNodeSpec{Count: n.Count, Role: n.Role}
	}
	if len(nodes) == 0 {
		nodes = []awsNodeSpec{{Count: 1, Role: []string{"etcd", "cp", "worker"}}}
	}

	if a.AWSSSHKeyName == "" {
		t.Fatalf("aws config: awsSSHKeyName is required")
	}
	privKeyPath, err := sshKeyFilePath(a.AWSSSHKeyName)
	if err != nil {
		t.Fatalf("resolve ssh key %q: %v", a.AWSSSHKeyName, err)
	}
	pubKeyPath, cleanup, err := derivePublicKeyFile(privKeyPath)
	if err != nil {
		t.Fatalf("derive public key from %q: %v", privKeyPath, err)
	}
	t.Cleanup(cleanup)

	nodeVars := awsClusterNodesVars{
		PublicSSHKey:   pubKeyPath,
		AccessKey:      a.AccessKey,
		SecretKey:      a.SecretKey,
		Region:         a.Region,
		AMI:            a.AMI,
		HostnamePrefix: hostnamePrefix,
		Route53Zone:    a.Route53Zone,
		SSHUser:        a.SSHUser,
		SecurityGroup:  a.SecurityGroups,
		VPC:            a.VPC,
		VolumeSize:     volumeSize,
		VolumeType:     volumeType,
		Subnet:         a.Subnet,
		InstanceType:   a.InstanceType,
		Nodes:          nodes,
		AirgapSetup:    a.AirgapSetup,
		ProxySetup:     a.ProxySetup,
	}

	nodeVarFile, err := writeTFVarsJSON(t, "aws-cluster-nodes-vars.json", nodeVars)
	if err != nil {
		t.Fatalf("write aws cluster nodes tfvars: %v", err)
	}

	nodeModuleDir := filepath.Join(repoPath, awsClusterNodesModulePath)
	nodeTofu := tofu.NewClient(nodeModuleDir, workspace)

	logrus.Infof("[qainfraautomation] tofu init (aws cluster nodes): %s", nodeModuleDir)
	if err := nodeTofu.Init(); err != nil {
		t.Fatalf("tofu init (aws cluster nodes): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu workspace select-or-create %q (aws cluster nodes)", workspace)
	if err := nodeTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (aws cluster nodes): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply (aws cluster nodes, workspace=%s)", workspace)
	if err := nodeTofu.Apply(nodeVarFile); err != nil {
		t.Fatalf("tofu apply (aws cluster nodes): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply complete (aws cluster nodes)")
	if cleanupEnabled(rancherClient) {
		t.Cleanup(func() {
			logrus.Infof("[qainfraautomation] destroying aws EC2 nodes (workspace=%s)", workspace)
			if err := nodeTofu.Destroy(nodeVarFile); err != nil {
				logrus.Errorf("[qainfraautomation] tofu destroy (aws EC2 nodes): %v", err)
			}
		})
	}

	generateName := clusterCfg.GenerateName
	if generateName == "" {
		generateName = hostnamePrefix
	}

	return provisionCustomClusterShared(t, rancherClient, cfg, clusterCfg, generateName,
		awsClusterNodesModulePath, privKeyPath, repoPath)
}

func provisionHarvesterCustomCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.CustomClusterConfig,
	repoPath string,
) *v1.SteveAPIObject {
	t.Helper()

	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	h := cfg.Harvester

	if h.SSHKeyName == "" {
		t.Fatalf("harvester config: sshKeyName is required")
	}
	privKeyPath, err := sshKeyFilePath(h.SSHKeyName)
	if err != nil {
		t.Fatalf("resolve ssh key %q: %v", h.SSHKeyName, err)
	}

	sshPubKeyContents, err := derivePublicKeyContents(privKeyPath)
	if err != nil {
		t.Fatalf("derive public key from %q: %v", privKeyPath, err)
	}

	if h.KubeConfigPath != "" {
		destKubeconfig := filepath.Join(repoPath, harvesterKubeconfigDest)
		logrus.Infof("[qainfraautomation] copying Harvester kubeconfig %s → %s", h.KubeConfigPath, destKubeconfig)
		if err := copyFile(h.KubeConfigPath, destKubeconfig); err != nil {
			t.Fatalf("copy harvester kubeconfig: %v", err)
		}
	}

	vmVars := buildHarvesterVMVars(h, clusterCfg.Nodes, sshPubKeyContents)
	vmVarFile, err := writeTFVarsJSON(t, "harvester-vm-vars.json", vmVars)
	if err != nil {
		t.Fatalf("write harvester VM tfvars: %v", err)
	}

	vmModuleDir := filepath.Join(repoPath, harvesterVMModulePath)
	vmTofu := tofu.NewClient(vmModuleDir, workspace)

	logrus.Infof("[qainfraautomation] tofu init (harvester vm): %s", vmModuleDir)
	if err := vmTofu.Init(); err != nil {
		t.Fatalf("tofu init (harvester vm): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu workspace select-or-create %q (harvester vm)", workspace)
	if err := vmTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (harvester vm): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply (harvester vm, workspace=%s)", workspace)
	if err := vmTofu.Apply(vmVarFile); err != nil {
		t.Fatalf("tofu apply (harvester vm): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply complete (harvester vm)")
	if cleanupEnabled(rancherClient) {
		t.Cleanup(func() {
			logrus.Infof("[qainfraautomation] destroying Harvester VMs (workspace=%s)", workspace)
			if err := vmTofu.Destroy(vmVarFile); err != nil {
				logrus.Errorf("[qainfraautomation] tofu destroy (Harvester VMs): %v", err)
			}
		})
	}

	generateName := clusterCfg.GenerateName
	if generateName == "" {
		generateName = "tf"
	}

	return provisionCustomClusterShared(t, rancherClient, cfg, clusterCfg, generateName,
		harvesterVMModulePath, privKeyPath, repoPath)
}

func provisionCustomClusterShared(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.CustomClusterConfig,
	generateName string,
	nodeModulePath string,
	sshPrivateKeyPath string,
	repoPath string,
) *v1.SteveAPIObject {
	t.Helper()

	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	clusterVars := rancherCustomClusterVars{
		KubernetesVersion: clusterCfg.KubernetesVersion,
		FQDN:              "https://" + rancherClient.RancherConfig.Host,
		APIKey:            rancherClient.RancherConfig.AdminToken,
		GenerateName:      generateName,
		IsNetworkPolicy:   clusterCfg.IsNetworkPolicy,
		PSA:               clusterCfg.PSA,
		Insecure:          *rancherClient.RancherConfig.Insecure,
	}

	clusterVarFile, err := writeTFVarsJSON(t, "rancher-custom-cluster-vars.json", clusterVars)
	if err != nil {
		t.Fatalf("write rancher custom cluster tfvars: %v", err)
	}

	clusterModuleDir := filepath.Join(repoPath, rancherCustomClusterModulePath)
	clusterTofu := tofu.NewClient(clusterModuleDir, workspace)

	logrus.Infof("[qainfraautomation] tofu init (rancher custom cluster): %s", clusterModuleDir)
	if err := clusterTofu.Init(); err != nil {
		t.Fatalf("tofu init (rancher custom cluster): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu workspace select-or-create %q (rancher custom cluster)", workspace)
	if err := clusterTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (rancher custom cluster): %v", err)
	}
	// -refresh=false: the rancher2 provider's kubeconfig fetch rejects newer Rancher ext/token format tokens.
	logrus.Infof("[qainfraautomation] tofu apply -refresh=false (rancher custom cluster, workspace=%s)", workspace)
	if err := clusterTofu.ApplyNoRefresh(clusterVarFile); err != nil {
		t.Fatalf("tofu apply (rancher custom cluster): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply complete (rancher custom cluster)")
	if cleanupEnabled(rancherClient) {
		t.Cleanup(func() {
			logrus.Infof("[qainfraautomation] destroying Rancher custom cluster resources (workspace=%s)", workspace)
			if err := clusterTofu.DestroyNoRefresh(clusterVarFile); err != nil {
				logrus.Errorf("[qainfraautomation] tofu destroy (rancher custom cluster): %v", err)
			}
		})
	}

	ansibleClient := ansible.NewClient(repoPath)

	inventoryEnv := map[string]string{
		"TERRAFORM_NODE_SOURCE": nodeModulePath,
		"TF_WORKSPACE":          workspace,
	}
	inventoryPath, err := ansibleClient.GenerateInventory(customClusterInventoryTemplate, inventoryEnv)
	if err != nil {
		t.Fatalf("generate inventory: %v", err)
	}

	if err := ansibleClient.AddSSHKey(sshPrivateKeyPath); err != nil {
		t.Fatalf("ssh-add: %v", err)
	}

	playbookEnv := []string{
		"TF_WORKSPACE=" + workspace,
		"TERRAFORM_NODE_SOURCE=" + nodeModulePath,
		"RANCHER_URL=https://" + rancherClient.RancherConfig.Host,
		"RANCHER_TOKEN=" + rancherClient.RancherConfig.AdminToken,
	}
	if *rancherClient.RancherConfig.Insecure {
		playbookEnv = append(playbookEnv, "RANCHER_INSECURE=true")
	}
	if clusterCfg.Harden {
		playbookEnv = append(playbookEnv, "HARDEN=true")
	}
	if ansibleCfg := resolveAnsibleConfig(repoPath, customClusterPlaybook, cfg); ansibleCfg != "" {
		playbookEnv = append(playbookEnv, "ANSIBLE_CONFIG="+ansibleCfg)
	}
	if err := ansibleClient.RunPlaybook(customClusterPlaybook, inventoryPath, playbookEnv); err != nil {
		t.Fatalf("ansible-playbook (custom cluster): %v", err)
	}

	clusterName, err := clusterTofu.Output("cluster_name")
	if err != nil {
		t.Fatalf("tofu output cluster_name: %v", err)
	}
	logrus.Infof("[qainfraautomation] cluster name from tofu: %s", clusterName)

	clusterObj, err := rancherClient.Steve.SteveType(stevetypes.Provisioning).ByID(fleetDefaultNamespace + "/" + clusterName)
	if err != nil {
		t.Fatalf("fetch cluster %q from Rancher after ready: %v", clusterName, err)
	}

	return clusterObj
}

// ProvisionHarvesterRKE2Cluster provisions a standalone RKE2 cluster on Harvester VMs and returns the kubeconfig path.
func ProvisionHarvesterRKE2Cluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
) string {
	t.Helper()
	return provisionHarvesterStandaloneCluster(t, rancherClient, cfg, clusterCfg, "rke2")
}

// ProvisionHarvesterK3SCluster provisions a standalone K3s cluster on Harvester VMs and returns the kubeconfig path.
func ProvisionHarvesterK3SCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
) string {
	t.Helper()
	return provisionHarvesterStandaloneCluster(t, rancherClient, cfg, clusterCfg, "k3s")
}

func provisionHarvesterStandaloneCluster(
	t *testing.T,
	rancherClient *rancher.Client,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
	clusterType string,
) string {
	t.Helper()

	repoPath := extractInfraFiles(t, false)
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	h := cfg.Harvester
	if h == nil {
		t.Fatalf("harvester config is required for standalone cluster provisioning")
	}

	if h.SSHKeyName == "" {
		t.Fatalf("harvester config: sshKeyName is required")
	}
	privKeyPath, err := sshKeyFilePath(h.SSHKeyName)
	if err != nil {
		t.Fatalf("resolve ssh key %q: %v", h.SSHKeyName, err)
	}

	sshPubKeyContents, err := derivePublicKeyContents(privKeyPath)
	if err != nil {
		t.Fatalf("derive public key from %q: %v", privKeyPath, err)
	}

	var playbookPath, inventoryTemplate, varsFile string
	switch clusterType {
	case "rke2":
		playbookPath = rke2Playbook
		inventoryTemplate = rke2InventoryTemplate
		varsFile = rke2VarsFile
	case "k3s":
		playbookPath = k3sPlaybook
		inventoryTemplate = k3sInventoryTemplate
		varsFile = k3sVarsFile
	default:
		t.Fatalf("unsupported cluster type: %s (must be rke2 or k3s)", clusterType)
	}

	destKubeconfig := filepath.Join(repoPath, harvesterKubeconfigDest)
	logrus.Infof("[qainfraautomation] copying Harvester kubeconfig %s → %s", h.KubeConfigPath, destKubeconfig)
	if err := copyFile(h.KubeConfigPath, destKubeconfig); err != nil {
		t.Fatalf("copy harvester kubeconfig: %v", err)
	}

	vmVars := buildHarvesterVMVars(h, clusterCfg.Nodes, sshPubKeyContents)
	vmVarFile, err := writeTFVarsJSON(t, "harvester-vm-vars.json", vmVars)
	if err != nil {
		t.Fatalf("write harvester VM tfvars: %v", err)
	}

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

	if cleanupEnabled(rancherClient) {
		t.Cleanup(func() {
			logrus.Infof("[qainfraautomation] destroying Harvester VMs (workspace=%s)", workspace)
			if err := vmTofu.Destroy(vmVarFile); err != nil {
				logrus.Errorf("[qainfraautomation] tofu destroy (harvester vm): %v", err)
			}
		})
	}

	ansibleClient := ansible.NewClient(repoPath)
	inventoryEnv := map[string]string{
		"TERRAFORM_NODE_SOURCE": harvesterVMModulePath,
		"TF_WORKSPACE":          workspace,
	}
	inventoryPath, err := ansibleClient.GenerateInventory(inventoryTemplate, inventoryEnv)
	if err != nil {
		t.Fatalf("generate inventory: %v", err)
	}

	vars := buildStandaloneVars(clusterCfg)
	if err := ansibleClient.WriteVarsYAML(varsFile, vars); err != nil {
		t.Fatalf("write vars.yaml: %v", err)
	}

	if err := ansibleClient.AddSSHKey(privKeyPath); err != nil {
		t.Fatalf("ssh-add: %v", err)
	}

	playbookEnv := []string{
		"TF_WORKSPACE=" + workspace,
		"TERRAFORM_NODE_SOURCE=" + harvesterVMModulePath,
	}
	if ansibleCfg := resolveAnsibleConfig(repoPath, playbookPath, cfg); ansibleCfg != "" {
		playbookEnv = append(playbookEnv, "ANSIBLE_CONFIG="+ansibleCfg)
	}
	if err := ansibleClient.RunPlaybook(playbookPath, inventoryPath, playbookEnv); err != nil {
		t.Fatalf("ansible-playbook (%s): %v", clusterType, err)
	}

	return clusterCfg.KubeconfigOutputPath
}

// ProvisionAWSRKE2Cluster provisions a standalone RKE2 cluster on AWS EC2 instances
// and returns the kubeconfig path and Route53 FQDN.
func ProvisionAWSRKE2Cluster(
	t *testing.T,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
) StandaloneClusterResult {
	t.Helper()
	return provisionAWSStandaloneCluster(t, cfg, clusterCfg, "rke2")
}

// ProvisionAWSK3SCluster provisions a standalone K3s cluster on AWS EC2 instances
// and returns the kubeconfig path and Route53 FQDN.
func ProvisionAWSK3SCluster(
	t *testing.T,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
) StandaloneClusterResult {
	t.Helper()
	return provisionAWSStandaloneCluster(t, cfg, clusterCfg, "k3s")
}

// StandaloneClusterResult holds the outputs from provisioning a standalone cluster.
type StandaloneClusterResult struct {
	KubeconfigPath string
	FQDN           string
}

func provisionAWSStandaloneCluster(
	t *testing.T,
	cfg *config.Config,
	clusterCfg *config.StandaloneClusterConfig,
	clusterType string,
) StandaloneClusterResult {
	t.Helper()

	repoPath := extractInfraFiles(t, !infraCleanupEnabled(cfg))
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	a := cfg.AWS
	if a == nil {
		t.Fatalf("aws config is required for standalone cluster provisioning")
	}

	if a.AWSSSHKeyName == "" {
		t.Fatalf("aws config: awsSSHKeyName is required")
	}
	privKeyPath, err := sshKeyFilePath(a.AWSSSHKeyName)
	if err != nil {
		t.Fatalf("resolve ssh key %q: %v", a.AWSSSHKeyName, err)
	}
	pubKeyPath, cleanup, err := derivePublicKeyFile(privKeyPath)
	if err != nil {
		t.Fatalf("derive public key from %q: %v", privKeyPath, err)
	}
	t.Cleanup(cleanup)

	hostnamePrefix := a.HostnamePrefix
	if hostnamePrefix == "" {
		hostnamePrefix = "tf"
	}
	volumeSize := a.VolumeSize
	if volumeSize == 0 {
		volumeSize = 50
	}
	volumeType := a.VolumeType
	if volumeType == "" {
		volumeType = "gp3"
	}

	nodes := make([]awsNodeSpec, len(clusterCfg.Nodes))
	for i, n := range clusterCfg.Nodes {
		nodes[i] = awsNodeSpec{Count: n.Count, Role: n.Role}
	}
	if len(nodes) == 0 {
		nodes = []awsNodeSpec{{Count: 1, Role: []string{"etcd", "cp", "worker"}}}
	}

	nodeVars := awsClusterNodesVars{
		PublicSSHKey:   pubKeyPath,
		AccessKey:      a.AccessKey,
		SecretKey:      a.SecretKey,
		Region:         a.Region,
		AMI:            a.AMI,
		HostnamePrefix: hostnamePrefix,
		Route53Zone:    a.Route53Zone,
		SSHUser:        a.SSHUser,
		SecurityGroup:  a.SecurityGroups,
		VPC:            a.VPC,
		VolumeSize:     volumeSize,
		VolumeType:     volumeType,
		Subnet:         a.Subnet,
		InstanceType:   a.InstanceType,
		Nodes:          nodes,
		AirgapSetup:    a.AirgapSetup,
		ProxySetup:     a.ProxySetup,
	}

	nodeVarFile, err := writeTFVarsJSON(t, "aws-standalone-nodes-vars.json", nodeVars)
	if err != nil {
		t.Fatalf("write aws standalone nodes tfvars: %v", err)
	}

	nodeModuleDir := filepath.Join(repoPath, awsClusterNodesModulePath)
	nodeTofu := tofu.NewClient(nodeModuleDir, workspace)

	logrus.Infof("[qainfraautomation] tofu init (aws standalone nodes): %s", nodeModuleDir)
	if err := nodeTofu.Init(); err != nil {
		t.Fatalf("tofu init (aws standalone nodes): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu workspace select-or-create %q (aws standalone nodes)", workspace)
	if err := nodeTofu.WorkspaceSelectOrCreate(); err != nil {
		t.Fatalf("tofu workspace (aws standalone nodes): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply (aws standalone nodes, workspace=%s)", workspace)
	if err := nodeTofu.Apply(nodeVarFile); err != nil {
		t.Fatalf("tofu apply (aws standalone nodes): %v", err)
	}
	logrus.Infof("[qainfraautomation] tofu apply complete (aws standalone nodes)")

	if infraCleanupEnabled(cfg) {
		t.Cleanup(func() {
			logrus.Infof("[qainfraautomation] destroying aws standalone EC2 nodes (workspace=%s)", workspace)
			if err := nodeTofu.Destroy(nodeVarFile); err != nil {
				logrus.Errorf("[qainfraautomation] tofu destroy (aws standalone EC2 nodes): %v", err)
			}
		})
	} else {
		logrus.Warn("[qainfraautomation] cleanup disabled: EC2 nodes will NOT be destroyed after test")
	}

	var playbookPath, inventoryTemplate, varsFile string
	switch clusterType {
	case "rke2":
		playbookPath = rke2Playbook
		inventoryTemplate = rke2InventoryTemplate
		varsFile = rke2VarsFile
	case "k3s":
		playbookPath = k3sPlaybook
		inventoryTemplate = k3sInventoryTemplate
		varsFile = k3sVarsFile
	default:
		t.Fatalf("unsupported cluster type: %s (must be rke2 or k3s)", clusterType)
	}

	ansibleClient := ansible.NewClient(repoPath)
	inventoryEnv := map[string]string{
		"TERRAFORM_NODE_SOURCE": awsClusterNodesModulePath,
		"TF_WORKSPACE":          workspace,
	}
	inventoryPath, err := ansibleClient.GenerateInventory(inventoryTemplate, inventoryEnv)
	if err != nil {
		t.Fatalf("generate inventory: %v", err)
	}

	vars := buildStandaloneVars(clusterCfg)
	if clusterCfg.ServerFlags != "" {
		vars["server_flags"] = clusterCfg.ServerFlags
	}
	if err := ansibleClient.WriteVarsYAML(varsFile, vars); err != nil {
		t.Fatalf("write vars.yaml: %v", err)
	}

	// Download any optional files (e.g. PSA config) that have URLs
	for _, of := range clusterCfg.OptionalFiles {
		if of.URL != "" {
			logrus.Infof("[qainfraautomation] downloading optional file %s -> %s", of.URL, of.Path)
			if err := downloadFile(of.URL, of.Path); err != nil {
				t.Fatalf("download optional file %s: %v", of.URL, err)
			}
		}
	}

	if err := ansibleClient.AddSSHKey(privKeyPath); err != nil {
		t.Fatalf("ssh-add: %v", err)
	}

	playbookEnv := []string{
		"TF_WORKSPACE=" + workspace,
		"TERRAFORM_NODE_SOURCE=" + awsClusterNodesModulePath,
	}
	if ansibleCfg := resolveAnsibleConfig(repoPath, playbookPath, cfg); ansibleCfg != "" {
		playbookEnv = append(playbookEnv, "ANSIBLE_CONFIG="+ansibleCfg)
	}
	if err := ansibleClient.RunPlaybook(playbookPath, inventoryPath, playbookEnv); err != nil {
		t.Fatalf("ansible-playbook (%s): %v", clusterType, err)
	}

	// Retrieve the Route53 FQDN from Tofu output
	fqdn, err := nodeTofu.Output("fqdn")
	if err != nil {
		logrus.Warnf("[qainfraautomation] could not retrieve fqdn output: %v", err)
	} else {
		logrus.Infof("[qainfraautomation] Route53 FQDN: %s", fqdn)
	}

	return StandaloneClusterResult{
		KubeconfigPath: clusterCfg.KubeconfigOutputPath,
		FQDN:           fqdn,
	}
}

// InstallRancherResult holds the outputs from a Rancher HA installation.
type InstallRancherResult struct {
	FQDN       string
	AdminToken string
}

// InstallRancher installs Rancher on an existing Kubernetes cluster using the
// default-ha Ansible playbook from qa-infra-automation. Returns the FQDN and
// admin API token on success.
func InstallRancher(
	t *testing.T,
	cfg *config.Config,
	kubeconfigPath string,
	fqdn string,
) InstallRancherResult {
	t.Helper()

	rancherCfg := cfg.RancherInstall
	if rancherCfg == nil {
		rancherCfg = &config.RancherInstallConfig{}
	}

	repoPath := extractInfraFiles(t, !infraCleanupEnabled(cfg))
	workspace := cfg.Workspace
	if workspace == "" {
		workspace = "default"
	}

	absKubeconfig, err := filepath.Abs(kubeconfigPath)
	if err != nil {
		t.Fatalf("resolve kubeconfig path %q: %v", kubeconfigPath, err)
	}

	vars := map[string]interface{}{
		"kubeconfig_file": absKubeconfig,
	}

	if fqdn != "" {
		vars["fqdn"] = fqdn
	}

	chartVersion := rancherCfg.ChartVersion
	if chartVersion == "" {
		chartVersion = "head"
	}
	vars["rancher_version"] = chartVersion

	if rancherCfg.ImageTag != "" {
		vars["rancher_image_tag"] = rancherCfg.ImageTag
	} else {
		vars["rancher_image_tag"] = ">=0.0.0-0"
	}

	if rancherCfg.CertManagerVersion != "" {
		vars["cert_manager_version"] = rancherCfg.CertManagerVersion
	} else {
		vars["cert_manager_version"] = ""
	}

	if rancherCfg.HelmRepo != "" {
		vars["rancher_chart_repo"] = rancherCfg.HelmRepo
	}
	if rancherCfg.HelmRepoURL != "" {
		vars["rancher_chart_repo_url"] = rancherCfg.HelmRepoURL
	}

	if rancherCfg.TLSSource != "" {
		vars["ingress_tls_source"] = rancherCfg.TLSSource
	}
	if rancherCfg.LetsEncryptEmail != "" {
		vars["letsencrypt_email"] = rancherCfg.LetsEncryptEmail
	}
	if rancherCfg.TLSCertPath != "" {
		absCert, err := filepath.Abs(rancherCfg.TLSCertPath)
		if err != nil {
			t.Fatalf("resolve tls cert path %q: %v", rancherCfg.TLSCertPath, err)
		}
		vars["tls_cert_path"] = absCert
	}
	if rancherCfg.TLSKeyPath != "" {
		absKey, err := filepath.Abs(rancherCfg.TLSKeyPath)
		if err != nil {
			t.Fatalf("resolve tls key path %q: %v", rancherCfg.TLSKeyPath, err)
		}
		vars["tls_key_path"] = absKey
	}
	if rancherCfg.TLSCACertPath != "" {
		absCA, err := filepath.Abs(rancherCfg.TLSCACertPath)
		if err != nil {
			t.Fatalf("resolve tls CA cert path %q: %v", rancherCfg.TLSCACertPath, err)
		}
		vars["tls_ca_cert_path"] = absCA
	}

	bootstrapPassword := rancherCfg.BootstrapPassword
	if bootstrapPassword == "" {
		bootstrapPassword = "admin"
	}
	vars["bootstrap_password"] = bootstrapPassword

	password := rancherCfg.Password
	if password == "" {
		password = bootstrapPassword
	}
	vars["password"] = password

	varsPath := filepath.Join(repoPath, rancherInstallVarsFile)
	if err := writeYAMLFile(varsPath, vars); err != nil {
		t.Fatalf("write rancher install vars: %v", err)
	}

	ansibleClient := ansible.NewClient(repoPath)

	playbookEnv := []string{
		"TF_WORKSPACE=" + workspace,
		"TERRAFORM_NODE_SOURCE=" + awsClusterNodesModulePath,
	}
	if ansibleCfg := resolveAnsibleConfig(repoPath, rancherInstallPlaybook, cfg); ansibleCfg != "" {
		playbookEnv = append(playbookEnv, "ANSIBLE_CONFIG="+ansibleCfg)
	} else {
		topLevelCfg := filepath.Join(repoPath, "ansible", "ansible.cfg")
		if _, err := os.Stat(topLevelCfg); err == nil {
			playbookEnv = append(playbookEnv, "ANSIBLE_CONFIG="+topLevelCfg)
		}
	}

	logrus.Info("[qainfraautomation] running Rancher HA install playbook")
	if err := ansibleClient.RunPlaybook(rancherInstallPlaybook, "localhost,", playbookEnv); err != nil {
		t.Fatalf("ansible-playbook (rancher install): %v", err)
	}

	generatedVarsPath := findGeneratedTFVars(repoPath)
	if generatedVarsPath == "" {
		t.Fatalf("parse generated.tfvars after rancher install: file not found (searched repoPath tree %s, $HOME, and CWD)", repoPath)
	}
	logrus.Infof("[qainfraautomation] found generated.tfvars at %s", generatedVarsPath)
	result, err := parseGeneratedTFVars(generatedVarsPath)
	if err != nil {
		t.Fatalf("parse generated.tfvars after rancher install: %v", err)
	}

	logrus.Infof("[qainfraautomation] Rancher installed successfully at %s", result.FQDN)
	return result
}

// writeYAMLFile marshals data to YAML and writes it to the given absolute path.
func writeYAMLFile(absPath string, data map[string]interface{}) error {
	logrus.Infof("[qainfraautomation] writing YAML file %s", absPath)

	if err := os.MkdirAll(filepath.Dir(absPath), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", absPath, err)
	}

	yamlBytes, err := yaml.Marshal(data)
	if err != nil {
		return fmt.Errorf("marshal YAML: %w", err)
	}

	return os.WriteFile(absPath, yamlBytes, 0644)
}

const generatedTFVarsFilename = "generated.tfvars"

// findGeneratedTFVars locates the generated.tfvars file written by the Rancher install
// playbook, searching $HOME, CWD, repoPath, and recursively under repoPath.
func findGeneratedTFVars(repoPath string) string {
	var candidates []string

	if homeDir, err := os.UserHomeDir(); err == nil {
		candidates = append(candidates, filepath.Join(homeDir, generatedTFVarsFilename))
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, generatedTFVarsFilename))
	}
	candidates = append(candidates, filepath.Join(repoPath, generatedTFVarsFilename))

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}

	var found string
	_ = filepath.WalkDir(repoPath, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Name() == generatedTFVarsFilename {
			found = path
			return filepath.SkipAll
		}
		return nil
	})
	return found
}

// parseGeneratedTFVars reads a generated.tfvars file and extracts the fqdn and api_key values.
func parseGeneratedTFVars(path string) (InstallRancherResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return InstallRancherResult{}, fmt.Errorf("read %s: %w", path, err)
	}

	var result InstallRancherResult
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.Trim(strings.TrimSpace(parts[1]), `"`)

		switch key {
		case "fqdn":
			result.FQDN = strings.TrimPrefix(strings.TrimPrefix(value, "https://"), "http://")
		case "api_key":
			result.AdminToken = value
		}
	}

	if result.FQDN == "" {
		return result, fmt.Errorf("fqdn not found in %s", path)
	}
	if result.AdminToken == "" {
		return result, fmt.Errorf("api_key not found in %s", path)
	}

	return result, nil
}

// downloadFile fetches a URL and writes it to the given path on disk.
func downloadFile(url, destPath string) error {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return fmt.Errorf("mkdir for %s: %w", destPath, err)
	}

	resp, err := http.Get(url)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("GET %s: HTTP %d", url, resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response body from %s: %w", url, err)
	}

	return os.WriteFile(destPath, data, 0644)
}

// resolveAnsibleConfig returns the ANSIBLE_CONFIG path for a playbook.
// Priority: explicit user config > ansible.cfg co-located with the playbook.
func resolveAnsibleConfig(repoPath string, playbookRelPath string, cfg *config.Config) string {
	if cfg.Ansible != nil && cfg.Ansible.ConfigPath != "" {
		return filepath.Join(repoPath, cfg.Ansible.ConfigPath)
	}

	playbookDir := filepath.Dir(filepath.Join(repoPath, playbookRelPath))
	candidate := filepath.Join(playbookDir, "ansible.cfg")
	if _, err := os.Stat(candidate); err == nil {
		return candidate
	}

	return ""
}

// buildStandaloneVars constructs the Ansible vars map for a standalone cluster,
// omitting empty values so that Ansible defaults are preserved.
func buildStandaloneVars(clusterCfg *config.StandaloneClusterConfig) map[string]string {
	vars := make(map[string]string)
	if clusterCfg.KubernetesVersion != "" {
		vars["kubernetes_version"] = clusterCfg.KubernetesVersion
	}
	if clusterCfg.CNI != "" {
		vars["cni"] = clusterCfg.CNI
	}
	if clusterCfg.Channel != "" {
		vars["channel"] = clusterCfg.Channel
	}
	if clusterCfg.KubeconfigOutputPath != "" {
		vars["kubeconfig_file"] = clusterCfg.KubeconfigOutputPath
	}
	return vars
}

func buildHarvesterVMVars(h *config.HarvesterConfig, g []config.CustomClusterNodeGroup, sshPubKey string) harvesterVMVars {
	nodes := make([]harvesterNodeSpec, len(g))
	for i, n := range g {
		nodes[i] = harvesterNodeSpec{
			Count: n.Count,
			Role:  n.Role,
		}
	}
	return harvesterVMVars{
		SSHKey:             sshPubKey,
		Nodes:              nodes,
		GenerateName:       h.GenerateName,
		SSHUser:            h.SSHUser,
		NetworkName:        h.NetworkName,
		BackendNetworkName: h.BackendNetworkName,
		ImageID:            h.ImageID,
		Namespace:          h.Namespace,
		CPU:                h.CPU,
		Memory:             h.Memory,
		DiskSize:           h.DiskSize,
		CreateLoadbalancer: h.CreateLoadbalancer,
		SubnetCIDR:         h.SubnetCIDR,
		GatewayIP:          h.GatewayIP,
		RangeIPStart:       h.RangeIPStart,
		RangeIPEnd:         h.RangeIPEnd,
		IPPoolName:         h.IPPoolName,
	}
}

func writeTFVarsJSON(t *testing.T, filename string, v any) (string, error) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal tfvars JSON: %w", err)
	}
	destPath := filepath.Join(t.TempDir(), filename)
	if err := os.WriteFile(destPath, data, 0600); err != nil {
		return "", fmt.Errorf("write tfvars file %s: %w", destPath, err)
	}
	logrus.Debugf("[qainfraautomation] wrote tfvars: %s", destPath)
	return destPath, nil
}

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

// sshKeyFilePath resolves an SSH key name to its full path on disk.
// It reads the sshPath config from shepherd and tries the key name
// as-is and with a .pem extension, returning the first path that exists.
func sshKeyFilePath(keyName string) (string, error) {
	sshDir := shepnodes.GetSSHPath().SSHPath

	candidates := []string{
		filepath.Join(sshDir, keyName),
		filepath.Join(sshDir, keyName+".pem"),
	}

	for _, p := range candidates {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}

	return "", fmt.Errorf("ssh key %q not found; tried %v", keyName, candidates)
}

// derivePublicKeyFile reads a PEM-encoded private key, derives the corresponding
// authorized-keys format public key, writes it to a temp file, and returns the
// file path along with a cleanup func to remove it.
func derivePublicKeyFile(privKeyPath string) (string, func(), error) {
	pubKey, err := derivePublicKeyContents(privKeyPath)
	if err != nil {
		return "", nil, err
	}

	f, err := os.CreateTemp("", "pub-*.pub")
	if err != nil {
		return "", nil, fmt.Errorf("create temp public key file: %w", err)
	}
	if _, err := f.WriteString(pubKey); err != nil {
		f.Close()
		os.Remove(f.Name())
		return "", nil, fmt.Errorf("write public key file: %w", err)
	}
	f.Close()

	return f.Name(), func() { os.Remove(f.Name()) }, nil
}

// derivePublicKeyContents reads a PEM-encoded private key and returns the
// corresponding public key in authorized-keys format as a string.
func derivePublicKeyContents(privKeyPath string) (string, error) {
	pemBytes, err := os.ReadFile(privKeyPath)
	if err != nil {
		return "", fmt.Errorf("read private key: %w", err)
	}

	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return "", fmt.Errorf("parse private key: %w", err)
	}

	return string(ssh.MarshalAuthorizedKey(signer.PublicKey())), nil
}

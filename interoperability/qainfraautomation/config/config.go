package config

// ConfigurationFileKey is the top-level key used in the cattle config YAML.
const ConfigurationFileKey = "qaInfraAutomation"

// Config is the top-level configuration struct for the qa-infra-automation integration.
type Config struct {
	// RepoPath is the absolute path to the rancher-qa-infra-automation repository on disk.
	// e.g. "/home/user/rancher-qa-infra-automation"
	RepoPath string `json:"repoPath" yaml:"repoPath"`

	// Workspace is the OpenTofu workspace name used for state isolation between test runs.
	// Defaults to "default" if empty.
	Workspace string `json:"workspace" yaml:"workspace"`

	// Harvester contains settings for the Harvester VM node provider.
	Harvester *HarvesterConfig `json:"harvester,omitempty" yaml:"harvester,omitempty"`

	// AWS contains settings for the AWS EC2 node provider.
	AWS *AWSConfig `json:"aws,omitempty" yaml:"aws,omitempty"`

	// CustomCluster holds parameters for the Rancher custom downstream cluster provisioning flow.
	CustomCluster *CustomClusterConfig `json:"customCluster,omitempty" yaml:"customCluster,omitempty"`

	// StandaloneCluster holds parameters for the standalone RKE2/K3S cluster provisioning flow.
	StandaloneCluster *StandaloneClusterConfig `json:"standaloneCluster,omitempty" yaml:"standaloneCluster,omitempty"`
}

// HarvesterConfig holds Harvester-specific settings used to provision VMs via OpenTofu.
type HarvesterConfig struct {
	// KubeConfigPath is the path to the Harvester kubeconfig file.
	// It will be copied to <repoPath>/tofu/harvester/modules/local.yaml as required by the HCL provider config.
	KubeConfigPath string `json:"kubeConfigPath" yaml:"kubeConfigPath"`

	// SSHPublicKey is the public SSH key injected into VMs via cloud-init.
	SSHPublicKey string `json:"sshPublicKey" yaml:"sshPublicKey"`

	// SSHPrivateKeyPath is the path to the private SSH key used by Ansible to connect to VMs.
	SSHPrivateKeyPath string `json:"sshPrivateKeyPath" yaml:"sshPrivateKeyPath"`

	// SSHUser is the default SSH user for the VM image (e.g. "ubuntu").
	SSHUser string `json:"sshUser,omitempty" yaml:"sshUser,omitempty"`

	// NetworkName is the Harvester network to attach VMs to.
	// e.g. "harvester-public/vlan2011"
	NetworkName string `json:"networkName,omitempty" yaml:"networkName,omitempty"`

	// ImageID is the Harvester image ID to use for VMs.
	// e.g. "harvester-public/noble-cloudimg-amd64"
	ImageID string `json:"imageId,omitempty" yaml:"imageId,omitempty"`

	// Namespace is the Harvester namespace to deploy resources into (default "default").
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`

	// GenerateName is the short name prefix appended to created resources (default "tf").
	GenerateName string `json:"generateName,omitempty" yaml:"generateName,omitempty"`

	// CPU is the number of vCPUs per VM (default 4).
	CPU int `json:"cpu,omitempty" yaml:"cpu,omitempty"`

	// Memory is the memory amount per VM (default "6Gi").
	Memory string `json:"memory,omitempty" yaml:"memory,omitempty"`

	// DiskSize is the disk size per VM (default "30Gi").
	DiskSize string `json:"diskSize,omitempty" yaml:"diskSize,omitempty"`

	// Nodes defines the VM node groups: count and roles per group.
	// Roles correspond to Rancher node roles: "etcd", "cp", "worker".
	Nodes []HarvesterNodeGroup `json:"nodes" yaml:"nodes"`
}

// HarvesterNodeGroup describes a set of identically-configured VMs sharing the same roles.
type HarvesterNodeGroup struct {
	// Count is the number of VMs in this group.
	Count int `json:"count" yaml:"count"`

	// Roles is the list of Rancher node roles for this group (e.g. ["etcd", "cp"]).
	Role []string `json:"role" yaml:"role"`
}

// AWSConfig holds AWS-specific settings for provisioning EC2 nodes via OpenTofu.
type AWSConfig struct {
	// Region is the AWS region to deploy into.
	Region string `json:"region,omitempty" yaml:"region,omitempty"`

	// SSHPrivateKeyPath is the path to the private SSH key used by Ansible.
	SSHPrivateKeyPath string `json:"sshPrivateKeyPath" yaml:"sshPrivateKeyPath"`

	// GenerateName is the short name prefix appended to created resources (default "tf").
	GenerateName string `json:"generateName,omitempty" yaml:"generateName,omitempty"`
}

// CustomClusterConfig holds parameters for provisioning a Rancher custom downstream cluster via Ansible.
type CustomClusterConfig struct {
	// KubernetesVersion is the RKE2/K3S kubernetes version string.
	// e.g. "v1.31.4+rke2r1"
	KubernetesVersion string `json:"kubernetesVersion" yaml:"kubernetesVersion"`

	// GenerateName is a short prefix used when naming the cluster in Rancher (default "tf").
	GenerateName string `json:"generateName,omitempty" yaml:"generateName,omitempty"`

	// IsNetworkPolicy enables network policy on the cluster.
	IsNetworkPolicy bool `json:"isNetworkPolicy,omitempty" yaml:"isNetworkPolicy,omitempty"`

	// PSA is the Pod Security Admission template name (e.g. "rancher-privileged").
	PSA string `json:"psa,omitempty" yaml:"psa,omitempty"`
}

// StandaloneClusterConfig holds parameters for provisioning a standalone RKE2 or K3S cluster via Ansible.
type StandaloneClusterConfig struct {
	// KubernetesVersion is the kubernetes version string passed to Ansible.
	KubernetesVersion string `json:"kubernetesVersion" yaml:"kubernetesVersion"`

	// CNI is the CNI plugin to use (e.g. "canal", "calico").
	CNI string `json:"cni,omitempty" yaml:"cni,omitempty"`

	// Channel is the release channel (e.g. "stable", "latest").
	Channel string `json:"channel,omitempty" yaml:"channel,omitempty"`

	// KubeconfigOutputPath is the local path where the kubeconfig will be written after cluster creation.
	KubeconfigOutputPath string `json:"kubeconfigOutputPath" yaml:"kubeconfigOutputPath"`
}

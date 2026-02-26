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

	// RancherCluster holds parameters for the Rancher-provisioned downstream cluster flow,
	// where Rancher itself provisions nodes via a cloud provider node driver.
	RancherCluster *RancherClusterConfig `json:"rancherCluster,omitempty" yaml:"rancherCluster,omitempty"`
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
	// AccessKey is the AWS access key ID.
	AccessKey string `json:"accessKey" yaml:"accessKey"`

	// SecretKey is the AWS secret access key.
	SecretKey string `json:"secretKey" yaml:"secretKey"`

	// Region is the AWS region to deploy into (e.g. "us-east-2").
	Region string `json:"region" yaml:"region"`

	// AMI is the EC2 AMI ID to use for nodes (e.g. "ami-01de4781572fa1285").
	AMI string `json:"ami" yaml:"ami"`

	// SSHUser is the default SSH user for the chosen AMI (e.g. "ec2-user", "ubuntu").
	SSHUser string `json:"sshUser" yaml:"sshUser"`

	// SSHPublicKeyPath is the path to the public SSH key file injected into EC2 instances.
	SSHPublicKeyPath string `json:"sshPublicKeyPath" yaml:"sshPublicKeyPath"`

	// SSHPrivateKeyPath is the path to the private SSH key used by Ansible to connect to nodes.
	SSHPrivateKeyPath string `json:"sshPrivateKeyPath" yaml:"sshPrivateKeyPath"`

	// InstanceType is the EC2 instance type (e.g. "t3a.xlarge").
	InstanceType string `json:"instanceType" yaml:"instanceType"`

	// VPC is the VPC ID (e.g. "vpc-0123456789abcdef0").
	VPC string `json:"vpc" yaml:"vpc"`

	// Subnet is the subnet ID (e.g. "subnet-0123456789abcdef0").
	Subnet string `json:"subnet" yaml:"subnet"`

	// SecurityGroups is a list of security group IDs (e.g. ["sg-0123456789abcdef0"]).
	SecurityGroups []string `json:"securityGroups" yaml:"securityGroups"`

	// VolumeSize is the root EBS volume size in GiB (e.g. 50).
	VolumeSize int `json:"volumeSize,omitempty" yaml:"volumeSize,omitempty"`

	// VolumeType is the EBS volume type (e.g. "gp3").
	VolumeType string `json:"volumeType,omitempty" yaml:"volumeType,omitempty"`

	// HostnamePrefix is the short prefix used to name EC2 instances and the Route53 record.
	// Also used as GenerateName for the Rancher custom cluster.
	HostnamePrefix string `json:"hostnamePrefix,omitempty" yaml:"hostnamePrefix,omitempty"`

	// Route53Zone is the Route53 hosted zone name used to register the cluster FQDN
	// (e.g. "qa.rancher.space").
	Route53Zone string `json:"route53Zone" yaml:"route53Zone"`

	// AirgapSetup disables public IP assignment on instances when true.
	AirgapSetup bool `json:"airgapSetup,omitempty" yaml:"airgapSetup,omitempty"`

	// ProxySetup disables public IP assignment on instances when true.
	ProxySetup bool `json:"proxySetup,omitempty" yaml:"proxySetup,omitempty"`

	// Nodes defines the EC2 node groups: count and Rancher roles per group.
	// Roles: "etcd", "cp", "worker".
	Nodes []AWSNodeGroup `json:"nodes" yaml:"nodes"`
}

// AWSNodeGroup describes a set of identically-configured EC2 instances sharing the same roles.
type AWSNodeGroup struct {
	// Count is the number of EC2 instances in this group.
	Count int `json:"count" yaml:"count"`

	// Role is the list of Rancher node roles for this group (e.g. ["etcd", "cp"] or ["worker"]).
	Role []string `json:"role" yaml:"role"`
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

// RancherClusterConfig holds parameters for provisioning a downstream cluster where Rancher itself
// provisions nodes via a cloud provider node driver (tofu/rancher/cluster module).
type RancherClusterConfig struct {
	// KubernetesVersion is the RKE2/K3S kubernetes version string.
	// e.g. "v1.31.4+rke2r1"
	KubernetesVersion string `json:"kubernetesVersion" yaml:"kubernetesVersion"`

	// GenerateName is a short prefix used when naming resources in Rancher (default "tf").
	GenerateName string `json:"generateName,omitempty" yaml:"generateName,omitempty"`

	// IsNetworkPolicy enables network policy on the cluster.
	IsNetworkPolicy bool `json:"isNetworkPolicy,omitempty" yaml:"isNetworkPolicy,omitempty"`

	// PSA is the Pod Security Admission template name (e.g. "rancher-privileged").
	PSA string `json:"psa,omitempty" yaml:"psa,omitempty"`

	// CloudProvider is the node driver to use. Supported values: "aws", "linode", "harvester".
	CloudProvider string `json:"cloudProvider" yaml:"cloudProvider"`

	// MachinePools defines the node pool topology for the cluster.
	// Each entry maps to one rancher2_cluster_v2 machine_pools block.
	MachinePools []RancherMachinePool `json:"machinePools,omitempty" yaml:"machinePools,omitempty"`

	// NodeConfig holds provider-specific node configuration passed verbatim to the tofu module
	// as the node_config variable. Use the provider-prefixed keys documented in the tofu module
	// (e.g. harvester_cpu_count, aws_instance_type, linode_instance_type).
	NodeConfig map[string]interface{} `json:"nodeConfig" yaml:"nodeConfig"`
}

// RancherMachinePool describes a single machine pool in a Rancher-provisioned cluster.
type RancherMachinePool struct {
	// ControlPlaneRole assigns the control-plane role to nodes in this pool.
	ControlPlaneRole bool `json:"controlPlaneRole,omitempty" yaml:"controlPlaneRole,omitempty"`

	// WorkerRole assigns the worker role to nodes in this pool.
	WorkerRole bool `json:"workerRole,omitempty" yaml:"workerRole,omitempty"`

	// EtcdRole assigns the etcd role to nodes in this pool.
	EtcdRole bool `json:"etcdRole,omitempty" yaml:"etcdRole,omitempty"`

	// Quantity is the number of nodes in this pool (default 1).
	Quantity int `json:"quantity,omitempty" yaml:"quantity,omitempty"`
}

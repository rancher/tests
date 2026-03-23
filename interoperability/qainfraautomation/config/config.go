package config

// ConfigurationFileKey is the top-level key used to unmarshal qaInfraAutomation config from the Rancher config file.
const ConfigurationFileKey = "qaInfraAutomation"

// Config holds all configuration required for QA infrastructure automation.
type Config struct {
	Workspace         string                   `json:"workspace" yaml:"workspace"`
	Harvester         *HarvesterConfig         `json:"harvester,omitempty" yaml:"harvester,omitempty"`
	AWS               *AWSConfig               `json:"aws,omitempty" yaml:"aws,omitempty"`
	CustomCluster     *CustomClusterConfig     `json:"customCluster,omitempty" yaml:"customCluster,omitempty"`
	StandaloneCluster *StandaloneClusterConfig `json:"standaloneCluster,omitempty" yaml:"standaloneCluster,omitempty"`
	RancherCluster    *RancherClusterConfig    `json:"rancherCluster,omitempty" yaml:"rancherCluster,omitempty"`
	Ansible           *AnsibleConfig           `json:"ansible,omitempty" yaml:"ansible,omitempty"`
}

// HarvesterConfig holds connection and VM configuration for a Harvester provider.
type HarvesterConfig struct {
	KubeConfigPath     string `json:"kubeConfigPath" yaml:"kubeConfigPath"`
	SSHPublicKey       string `json:"sshPublicKey" yaml:"sshPublicKey"`
	SSHPrivateKeyPath  string `json:"sshPrivateKeyPath" yaml:"sshPrivateKeyPath"`
	SSHUser            string `json:"sshUser,omitempty" yaml:"sshUser,omitempty"`
	NetworkName        string `json:"networkName,omitempty" yaml:"networkName,omitempty"`
	ImageID            string `json:"imageId,omitempty" yaml:"imageId,omitempty"`
	Namespace          string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	GenerateName       string `json:"generateName,omitempty" yaml:"generateName,omitempty"`
	CPU                int    `json:"cpu,omitempty" yaml:"cpu,omitempty"`
	Memory             string `json:"memory,omitempty" yaml:"memory,omitempty"`
	DiskSize           string `json:"diskSize,omitempty" yaml:"diskSize,omitempty"`
	CreateLoadbalancer bool   `json:"createLoadbalancer,omitempty" yaml:"createLoadbalancer,omitempty"`
	BackendNetworkName string `json:"backendNetworkName,omitempty" yaml:"backendNetworkName,omitempty"`
	SubnetCIDR         string `json:"subnetCidr,omitempty" yaml:"subnetCidr,omitempty"`
	GatewayIP          string `json:"gatewayIp,omitempty" yaml:"gatewayIp,omitempty"`
	RangeIPStart       string `json:"rangeIpStart,omitempty" yaml:"rangeIpStart,omitempty"`
	RangeIPEnd         string `json:"rangeIpEnd,omitempty" yaml:"rangeIpEnd,omitempty"`
	IPPoolName         string `json:"ippoolName,omitempty" yaml:"ippoolName,omitempty"`
}

// AWSConfig holds credentials and instance settings for an AWS provider.
type AWSConfig struct {
	AccessKey         string   `json:"accessKey" yaml:"accessKey"`
	SecretKey         string   `json:"secretKey" yaml:"secretKey"`
	Region            string   `json:"region" yaml:"region"`
	AMI               string   `json:"ami" yaml:"ami"`
	SSHUser           string   `json:"sshUser" yaml:"sshUser"`
	AWSSSHKeyName     string   `json:"awsSSHKeyName" yaml:"awsSSHKeyName"`
	SSHPrivateKeyPath string   `json:"sshPrivateKeyPath" yaml:"sshPrivateKeyPath"`
	InstanceType      string   `json:"instanceType" yaml:"instanceType"`
	VPC               string   `json:"vpc" yaml:"vpc"`
	Subnet            string   `json:"subnet" yaml:"subnet"`
	SecurityGroups    []string `json:"securityGroups" yaml:"securityGroups"`
	VolumeSize        int      `json:"volumeSize,omitempty" yaml:"volumeSize,omitempty"`
	VolumeType        string   `json:"volumeType,omitempty" yaml:"volumeType,omitempty"`
	HostnamePrefix    string   `json:"hostnamePrefix,omitempty" yaml:"hostnamePrefix,omitempty"`
	Route53Zone       string   `json:"route53Zone" yaml:"route53Zone"`
	AirgapSetup       bool     `json:"airgapSetup,omitempty" yaml:"airgapSetup,omitempty"`
	ProxySetup        bool     `json:"proxySetup,omitempty" yaml:"proxySetup,omitempty"`
}

// CustomClusterNodeGroup specifies the count and roles for a group of nodes in a custom cluster.
type CustomClusterNodeGroup struct {
	Count int      `json:"count" yaml:"count"`
	Role  []string `json:"role" yaml:"role"`
}

// CustomClusterConfig describes a custom (node-driver) cluster to provision.
type CustomClusterConfig struct {
	KubernetesVersion string                   `json:"kubernetesVersion" yaml:"kubernetesVersion"`
	GenerateName      string                   `json:"generateName,omitempty" yaml:"generateName,omitempty"`
	IsNetworkPolicy   bool                     `json:"isNetworkPolicy,omitempty" yaml:"isNetworkPolicy,omitempty"`
	PSA               string                   `json:"psa,omitempty" yaml:"psa,omitempty"`
	Harden            bool                     `json:"harden,omitempty" yaml:"harden,omitempty"`
	Nodes             []CustomClusterNodeGroup `json:"nodes" yaml:"nodes"`
}

// StandaloneClusterConfig describes a standalone (non-Rancher-managed) cluster to provision.
type StandaloneClusterConfig struct {
	KubernetesVersion    string                   `json:"kubernetesVersion" yaml:"kubernetesVersion"`
	CNI                  string                   `json:"cni,omitempty" yaml:"cni,omitempty"`
	Channel              string                   `json:"channel,omitempty" yaml:"channel,omitempty"`
	KubeconfigOutputPath string                   `json:"kubeconfigOutputPath" yaml:"kubeconfigOutputPath"`
	Nodes                []CustomClusterNodeGroup `json:"nodes" yaml:"nodes"`
}

// RancherClusterConfig describes a Rancher-managed cluster to provision via the Rancher API.
type RancherClusterConfig struct {
	KubernetesVersion string                 `json:"kubernetesVersion" yaml:"kubernetesVersion"`
	GenerateName      string                 `json:"generateName,omitempty" yaml:"generateName,omitempty"`
	IsNetworkPolicy   bool                   `json:"isNetworkPolicy,omitempty" yaml:"isNetworkPolicy,omitempty"`
	PSA               string                 `json:"psa,omitempty" yaml:"psa,omitempty"`
	CloudProvider     string                 `json:"cloudProvider" yaml:"cloudProvider"`
	MachinePools      []RancherMachinePool   `json:"machinePools,omitempty" yaml:"machinePools,omitempty"`
	NodeConfig        map[string]interface{} `json:"nodeConfig" yaml:"nodeConfig"`
}

// RancherMachinePool defines the role and size of a single machine pool in a Rancher cluster.
type RancherMachinePool struct {
	ControlPlaneRole bool `json:"controlPlaneRole,omitempty" yaml:"controlPlaneRole,omitempty"`
	WorkerRole       bool `json:"workerRole,omitempty" yaml:"workerRole,omitempty"`
	EtcdRole         bool `json:"etcdRole,omitempty" yaml:"etcdRole,omitempty"`
	Quantity         int  `json:"quantity,omitempty" yaml:"quantity,omitempty"`
}

// AnsibleConfig holds optional Ansible configuration for playbook execution.
type AnsibleConfig struct {
	ConfigPath string `json:"configPath,omitempty" yaml:"configPath,omitempty"`
}

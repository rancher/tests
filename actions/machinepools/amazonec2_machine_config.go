package machinepools

import (
	"slices"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/nodes/ec2"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

const (
	AWSKind           = "Amazonec2Config"
	AWSPoolType       = "rke-machine-config.cattle.io.amazonec2config"
	AWSResourceConfig = "amazonec2configs"
)

type AWSMachineConfigs struct {
	AWSMachineConfig []AWSMachineConfig `json:"awsMachineConfig" yaml:"awsMachineConfig"`
	Region           string             `json:"region" yaml:"region"`
}

// AWSMachineConfig is configuration needed to create an rke-machine-config.cattle.io.amazonec2config
type AWSMachineConfig struct {
	Roles
	AMI                string   `json:"ami" yaml:"ami"`
	EnablePrimaryIpv6  bool     `json:"enablePrimaryIpv6" yaml:"enablePrimaryIpv6"`
	HttpProtocolIpv6   string   `json:"httpProtocolIpv6" yaml:"httpProtocolIpv6"`
	IAMInstanceProfile string   `json:"iamInstanceProfile" yaml:"iamInstanceProfile"`
	InstanceType       string   `json:"instanceType" yaml:"instanceType"`
	Ipv6AddressCount   string   `json:"ipv6AddressCount" yaml:"ipv6AddressCount"`
	Ipv6AddressOnly    bool     `json:"ipv6AddressOnly" yaml:"ipv6AddressOnly"`
	PrivateAddressOnly bool     `json:"privateAddressOnly" yaml:"privateAddressOnly"`
	Retries            string   `json:"retries" yaml:"retries"`
	RootSize           string   `json:"rootSize" yaml:"rootSize"`
	SecurityGroup      []string `json:"securityGroup" yaml:"securityGroup"`
	SSHUser            string   `json:"sshUser" yaml:"sshUser"`
	SubnetID           string   `json:"subnetId" yaml:"subnetId"`
	UsePrivateAddress  bool     `json:"usePrivateAddress" yaml:"usePrivateAddress"`
	VPCID              string   `json:"vpcId" yaml:"vpcId"`
	VolumeType         string   `json:"volumeType" yaml:"volumeType"`
	Zone               string   `json:"zone" yaml:"zone"`
}

// LoadAWSMachineConfig loads the awsMachineConfigs from a provided cattle config file
func LoadAWSMachineConfig(cattleConfig map[string]any) MachineConfigs {
	var machineConfigs MachineConfigs

	awsMachineConfigs := new(AWSMachineConfigs)
	operations.LoadObjectFromMap(AWSMachineConfigsKey, cattleConfig, awsMachineConfigs)
	machineConfigs.AmazonEC2MachineConfigs = awsMachineConfigs

	return machineConfigs
}

// NewAWSMachineConfig is a constructor to set up rke-machine-config.cattle.io.amazonec2config. It returns an *unstructured.Unstructured
// that CreateMachineConfig uses to created the rke-machine-config
func NewAWSMachineConfig(machineConfigs MachineConfigs, generatedPoolName, namespace string) []unstructured.Unstructured {
	var multiConfig []unstructured.Unstructured
	for _, awsMachineConfig := range machineConfigs.AmazonEC2MachineConfigs.AWSMachineConfig {
		machineConfig := unstructured.Unstructured{}
		machineConfig.SetAPIVersion("rke-machine-config.cattle.io/v1")
		machineConfig.SetKind(AWSKind)
		machineConfig.SetGenerateName(generatedPoolName)
		machineConfig.SetNamespace(namespace)

		machineConfig.Object["ami"] = awsMachineConfig.AMI
		machineConfig.Object["enablePrimaryIpv6"] = awsMachineConfig.EnablePrimaryIpv6
		machineConfig.Object["httpProtocolIpv6"] = awsMachineConfig.HttpProtocolIpv6
		machineConfig.Object["iamInstanceProfile"] = awsMachineConfig.IAMInstanceProfile
		machineConfig.Object["instanceType"] = awsMachineConfig.InstanceType
		machineConfig.Object["ipv6AddressCount"] = awsMachineConfig.Ipv6AddressCount
		machineConfig.Object["ipv6AddressOnly"] = awsMachineConfig.Ipv6AddressOnly
		machineConfig.Object["privateAddressOnly"] = awsMachineConfig.PrivateAddressOnly
		machineConfig.Object["region"] = machineConfigs.AmazonEC2MachineConfigs.Region
		machineConfig.Object["retries"] = awsMachineConfig.Retries
		machineConfig.Object["rootSize"] = awsMachineConfig.RootSize
		machineConfig.Object["securityGroup"] = awsMachineConfig.SecurityGroup
		machineConfig.Object["sshUser"] = awsMachineConfig.SSHUser
		machineConfig.Object["subnetId"] = awsMachineConfig.SubnetID
		machineConfig.Object["type"] = AWSPoolType
		machineConfig.Object["usePrivateAddress"] = awsMachineConfig.UsePrivateAddress
		machineConfig.Object["volumeType"] = awsMachineConfig.VolumeType
		machineConfig.Object["vpcId"] = awsMachineConfig.VPCID
		machineConfig.Object["zone"] = awsMachineConfig.Zone

		multiConfig = append(multiConfig, machineConfig)
	}

	return multiConfig
}

// GetAWSMachineRoles returns a list of roles from the given machineConfigs
func GetAWSMachineRoles(machineConfigs MachineConfigs) []Roles {
	var allRoles []Roles
	for _, awsMachineConfig := range machineConfigs.AmazonEC2MachineConfigs.AWSMachineConfig {
		allRoles = append(allRoles, awsMachineConfig.Roles)
	}

	return allRoles
}

// GetAWSOSNames connects to aws and converts each ami in the machineConfigs into the associated aws name
func GetAWSOSNames(client *rancher.Client, cloudCredential cloudcredentials.CloudCredential, machineConfigs MachineConfigs) ([]string, error) {
	var osNames []string
	for _, machineConfig := range machineConfigs.AmazonEC2MachineConfigs.AWSMachineConfig {
		amiInfo, err := ec2.GetAMI(client, cloudCredential.AmazonEC2CredentialConfig, machineConfig.AMI)
		if err != nil {
			return nil, err
		}

		osName := *amiInfo.Images[0].Name
		if !slices.Contains(osNames, osName) {
			osNames = append(osNames, osName)
		}
	}

	return osNames, nil
}

package provisioning

import (
	"fmt"
	"slices"

	rancherEc2 "github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/tests/actions/nodes/ec2"
)

const (
	ec2NodeProviderName = "ec2"
	fromConfig          = "config"
)

type NodeCreationFunc func(client *rancher.Client, rolesPerPool []string, quantityPerPool []int32) (nodes []*nodes.Node, err error)
type NodeDeletionFunc func(client *rancher.Client, nodes []*nodes.Node) error
type CustomOSNamesFunc func(client *rancher.Client, customConfig rancherEc2.AWSEC2Configs) ([]string, error)

type ExternalNodeProvider struct {
	Name             string
	NodeCreationFunc NodeCreationFunc
	NodeDeletionFunc NodeDeletionFunc
	GetOSNamesFunc   CustomOSNamesFunc
}

// ExternalNodeProviderSetup is a helper function that setups an ExternalNodeProvider object is a wrapper
// for the specific outside node provider node creator function
func ExternalNodeProviderSetup(providerType string) ExternalNodeProvider {
	switch providerType {
	case ec2NodeProviderName:
		return ExternalNodeProvider{
			Name:             providerType,
			NodeCreationFunc: ec2.CreateNodes,
			NodeDeletionFunc: ec2.DeleteNodes,
			GetOSNamesFunc:   GetAWSOSNames,
		}
	case fromConfig:
		return ExternalNodeProvider{
			Name: providerType,
			NodeCreationFunc: func(client *rancher.Client, rolesPerPool []string, quantityPerPool []int32) (nodesList []*nodes.Node, err error) {
				var nodeConfig nodes.ExternalNodeConfig
				config.LoadConfig(nodes.ExternalNodeConfigConfigurationFileKey, &nodeConfig)

				nodesList = nodeConfig.Nodes[-1]

				for _, node := range nodesList {
					sshKey, err := nodes.GetSSHKey(node.SSHKeyName)
					if err != nil {
						return nil, err
					}

					node.SSHKey = sshKey
				}
				return nodesList, nil
			},
			NodeDeletionFunc: func(client *rancher.Client, nodes []*nodes.Node) error {
				return ec2.DeleteNodes(client, nodes)
			},
		}
	default:
		panic(fmt.Sprintf("Node Provider:%v not found", providerType))
	}

}

// GetAWSOSNames connects to aws and converts each ami in the machineConfigs into the associated aws name
func GetAWSOSNames(client *rancher.Client, customConfig rancherEc2.AWSEC2Configs) ([]string, error) {
	cloudCredential := cloudcredentials.AmazonEC2CredentialConfig{
		AccessKey:     customConfig.AWSAccessKeyID,
		SecretKey:     customConfig.AWSSecretAccessKey,
		DefaultRegion: customConfig.Region,
	}

	var osNames []string
	for _, ec2Config := range customConfig.AWSEC2Config {
		amiInfo, err := ec2.GetAMI(client, &cloudCredential, ec2Config.AWSAMI)
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

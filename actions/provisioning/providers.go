package provisioning

import (
	"fmt"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/cloudcredentials/aws"
	"github.com/rancher/shepherd/extensions/cloudcredentials/azure"
	"github.com/rancher/shepherd/extensions/cloudcredentials/digitalocean"
	"github.com/rancher/shepherd/extensions/cloudcredentials/harvester"
	"github.com/rancher/shepherd/extensions/cloudcredentials/linode"
	"github.com/rancher/shepherd/extensions/cloudcredentials/vsphere"
	"github.com/rancher/tests/actions/cloudprovider"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioninginput"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

type ProviderName string

const (
	AWSProvider          = "aws"
	AzureProvider        = "azure"
	DOProvider           = "do"
	HarvesterProvider    = "harvester"
	LinodeProvider       = "linode"
	GoogleProvider       = "google"
	VsphereProvider      = "vsphere"
	VsphereCloudProvider = "rancher-vsphere"
)

type CloudCredFunc func(rancherClient *rancher.Client, credentials cloudcredentials.CloudCredential) (*v1.SteveAPIObject, error)
type LoadMachineConfigFunc func(cattleConfig map[string]any) machinepools.MachineConfigs
type LoadCredentialConfigFunc func(cattleConfig map[string]any) (cloudcredentials.CloudCredential, error)
type MachinePoolFunc func(machineConfig machinepools.MachineConfigs, generatedPoolName, namespace string) []unstructured.Unstructured
type MachineRolesFunc func(machineConfig machinepools.MachineConfigs) []machinepools.Roles
type OSNamesFunc func(client *rancher.Client, cloudCredential cloudcredentials.CloudCredential, machineConfigs machinepools.MachineConfigs) ([]string, error)
type VerifyCloudProviderFunc func(t *testing.T, client *rancher.Client, clusterObject *steveV1.SteveAPIObject)

type Provider struct {
	Name                               provisioninginput.ProviderName
	CloudProviderName                  string
	MachineConfigPoolResourceSteveType string
	LoadMachineConfigFunc              LoadMachineConfigFunc
	LoadCredentialConfigFunc           LoadCredentialConfigFunc
	MachinePoolFunc                    MachinePoolFunc
	CloudCredFunc                      CloudCredFunc
	VerifyCloudProviderFunc            VerifyCloudProviderFunc
	GetMachineRolesFunc                MachineRolesFunc
	GetOSNamesFunc                     OSNamesFunc
}

// GetProviderFromConfig returns the Terraform and downstream providers, each defaulting to AWS.
// The downstream provider prefers clusterConfig over terraform.downstreamClusterProvider.
func GetProviderFromConfig(cattleConfig map[string]any) (string, string, error) {
	clusterConfig, _ := cattleConfig["clusterConfig"].(map[string]any)
	terraformConfig, _ := cattleConfig["terraform"].(map[string]any)
	clusterProvider, _ := clusterConfig["provider"].(string)
	terraformProvider, _ := terraformConfig["provider"].(string)
	if clusterProvider == "" {
		clusterProvider, _ = terraformConfig["downstreamClusterProvider"].(string)
	}

	terraformProvider, err := getProviderName(terraformProvider)
	if err != nil {
		return "", "", fmt.Errorf("terraform provider: %w", err)
	}

	downstreamProvider, err := getProviderName(clusterProvider)
	if err != nil {
		return "", "", fmt.Errorf("downstream cluster provider: %w", err)
	}

	return terraformProvider, downstreamProvider, nil
}

func getProviderName(provider string) (string, error) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || defaults.IsSinglePlaceholderString(provider) {
		return AWSProvider, nil
	}

	for _, supportedProvider := range []string{AWSProvider, VsphereProvider, AzureProvider, HarvesterProvider, LinodeProvider, GoogleProvider, DOProvider} {
		if strings.Contains(provider, supportedProvider) {
			return supportedProvider, nil
		}
	}

	return "", fmt.Errorf("configured provider %q is not supported", provider)
}

// CreateProvider returns all machine and cloud credential
// configs in the form of a Provider struct. Accepts a
// string of the name of the provider.
func CreateProvider(name string) Provider {
	var provider Provider
	switch {
	case name == AWSProvider:
		provider = Provider{
			Name:                               AWSProvider,
			CloudProviderName:                  AWSProvider,
			MachineConfigPoolResourceSteveType: machinepools.AWSPoolType,
			LoadMachineConfigFunc:              machinepools.LoadAWSMachineConfig,
			LoadCredentialConfigFunc:           aws.LoadAWSCredentialFromConfig,
			MachinePoolFunc:                    machinepools.NewAWSMachineConfig,
			CloudCredFunc:                      aws.CreateAWSCloudCredentials,
			VerifyCloudProviderFunc:            cloudprovider.VerifyAWSCloudProvider,
			GetMachineRolesFunc:                machinepools.GetAWSMachineRoles,
			GetOSNamesFunc:                     machinepools.GetAWSOSNames,
		}
	case name == AzureProvider:
		provider = Provider{
			Name:                               AzureProvider,
			MachineConfigPoolResourceSteveType: machinepools.AzurePoolType,
			LoadMachineConfigFunc:              machinepools.LoadAzureMachineConfig,
			LoadCredentialConfigFunc:           azure.LoadAzureCredentialFromConfig,
			MachinePoolFunc:                    machinepools.NewAzureMachineConfig,
			CloudCredFunc:                      azure.CreateAzureCloudCredentials,
			GetMachineRolesFunc:                machinepools.GetAzureMachineRoles,
		}
	case name == DOProvider:
		provider = Provider{
			Name:                               DOProvider,
			MachineConfigPoolResourceSteveType: machinepools.DOPoolType,
			LoadMachineConfigFunc:              machinepools.LoadDOMachineConfig,
			LoadCredentialConfigFunc:           digitalocean.LoadDigitalOceanCredentialFromConfig,
			MachinePoolFunc:                    machinepools.NewDigitalOceanMachineConfig,
			CloudCredFunc:                      digitalocean.CreateDigitalOceanCloudCredentials,
			GetMachineRolesFunc:                machinepools.GetDOMachineRoles,
		}
	case name == LinodeProvider:
		provider = Provider{
			Name:                               LinodeProvider,
			MachineConfigPoolResourceSteveType: machinepools.LinodePoolType,
			LoadMachineConfigFunc:              machinepools.LoadLinodeMachineConfig,
			LoadCredentialConfigFunc:           linode.LoadLinodeCredentialFromConfig,
			MachinePoolFunc:                    machinepools.NewLinodeMachineConfig,
			CloudCredFunc:                      linode.CreateLinodeCloudCredentials,
			GetMachineRolesFunc:                machinepools.GetLinodeMachineRoles,
		}
	case name == HarvesterProvider:
		provider = Provider{
			Name:                               HarvesterProvider,
			CloudProviderName:                  HarvesterProvider,
			MachineConfigPoolResourceSteveType: machinepools.HarvesterPoolType,
			LoadMachineConfigFunc:              machinepools.LoadHarvesterMachineConfig,
			LoadCredentialConfigFunc:           harvester.LoadHarvesterCredentialFromConfig,
			MachinePoolFunc:                    machinepools.NewHarvesterMachineConfig,
			CloudCredFunc:                      harvester.CreateHarvesterCloudCredentials,
			VerifyCloudProviderFunc:            cloudprovider.VerifyHarvesterCloudProvider,
			GetMachineRolesFunc:                machinepools.GetHarvesterMachineRoles,
		}
	case name == VsphereProvider:
		provider = Provider{
			Name:                               VsphereProvider,
			CloudProviderName:                  VsphereCloudProvider,
			MachineConfigPoolResourceSteveType: machinepools.VmwarevsphereType,
			LoadMachineConfigFunc:              machinepools.LoadVSphereMachineConfig,
			LoadCredentialConfigFunc:           vsphere.LoadVSphereCredentialFromConfig,
			MachinePoolFunc:                    machinepools.NewVSphereMachineConfig,
			CloudCredFunc:                      vsphere.CreateVsphereCloudCredentials,
			VerifyCloudProviderFunc:            cloudprovider.VerifyVSphereCloudProvider,
			GetMachineRolesFunc:                machinepools.GetVsphereMachineRoles,
		}
	default:
		panic(fmt.Sprintf("Provider:%v not found", name))
	}

	return provider
}

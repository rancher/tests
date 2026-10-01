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
type MachinePoolFunc func(machineConfig machinepools.MachineConfigs, generatedPoolName, namespace string) []unstructured.Unstructured
type MachineRolesFunc func(machineConfig machinepools.MachineConfigs) []machinepools.Roles
type OSNamesFunc func(client *rancher.Client, cloudCredential cloudcredentials.CloudCredential, machineConfigs machinepools.MachineConfigs) ([]string, error)
type VerifyCloudProviderFunc func(t *testing.T, client *rancher.Client, clusterObject *steveV1.SteveAPIObject)

type Provider struct {
	Name                               provisioninginput.ProviderName
	CloudProviderName                  string
	MachineConfigPoolResourceSteveType string
	LoadMachineConfigFunc              LoadMachineConfigFunc
	MachinePoolFunc                    MachinePoolFunc
	CloudCredFunc                      CloudCredFunc
	VerifyCloudProviderFunc            VerifyCloudProviderFunc
	GetMachineRolesFunc                MachineRolesFunc
	GetOSNamesFunc                     OSNamesFunc
}

// GetProviderFromConfig returns the common provider contained in both
// clusterConfig.provider and terraform.provider, or an error if none exists.
func GetProviderFromConfig(cattleConfig map[string]any) (string, error) {
	clusterConfig, _ := cattleConfig["clusterConfig"].(map[string]any)
	terraformConfig, _ := cattleConfig["terraform"].(map[string]any)
	clusterProvider, _ := clusterConfig["provider"].(string)
	terraformProvider, _ := terraformConfig["provider"].(string)
	clusterProvider = strings.ToLower(clusterProvider)
	terraformProvider = strings.ToLower(terraformProvider)

	switch {
	case strings.Contains(clusterProvider, AWSProvider) && strings.Contains(terraformProvider, AWSProvider):
		return AWSProvider, nil
	case strings.Contains(clusterProvider, VsphereProvider) && strings.Contains(terraformProvider, VsphereProvider):
		return VsphereProvider, nil
	case strings.Contains(clusterProvider, AzureProvider) && strings.Contains(terraformProvider, AzureProvider):
		return AzureProvider, nil
	case strings.Contains(clusterProvider, HarvesterProvider) && strings.Contains(terraformProvider, HarvesterProvider):
		return HarvesterProvider, nil
	case strings.Contains(clusterProvider, LinodeProvider) && strings.Contains(terraformProvider, LinodeProvider):
		return LinodeProvider, nil
	case strings.Contains(clusterProvider, GoogleProvider) && strings.Contains(terraformProvider, GoogleProvider):
		return GoogleProvider, nil
	case strings.Contains(clusterProvider, DOProvider) && strings.Contains(terraformProvider, DOProvider):
		return DOProvider, nil
	default:
		return "", fmt.Errorf("clusterConfig.provider %q and terraform.provider %q do not contain a common supported provider", clusterProvider, terraformProvider)
	}
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
			MachinePoolFunc:                    machinepools.NewAzureMachineConfig,
			CloudCredFunc:                      azure.CreateAzureCloudCredentials,
			GetMachineRolesFunc:                machinepools.GetAzureMachineRoles,
		}
	case name == DOProvider:
		provider = Provider{
			Name:                               DOProvider,
			MachineConfigPoolResourceSteveType: machinepools.DOPoolType,
			LoadMachineConfigFunc:              machinepools.LoadDOMachineConfig,
			MachinePoolFunc:                    machinepools.NewDigitalOceanMachineConfig,
			CloudCredFunc:                      digitalocean.CreateDigitalOceanCloudCredentials,
			GetMachineRolesFunc:                machinepools.GetDOMachineRoles,
		}
	case name == LinodeProvider:
		provider = Provider{
			Name:                               LinodeProvider,
			MachineConfigPoolResourceSteveType: machinepools.LinodePoolType,
			LoadMachineConfigFunc:              machinepools.LoadLinodeMachineConfig,
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

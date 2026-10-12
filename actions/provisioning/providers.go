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

// GetDefaultFileName selects provider-specific defaults from the cluster or Terraform config.
func GetDefaultFileName(cattleConfig map[string]any) (string, error) {
	providerName := ""
	for _, section := range []string{"clusterConfig", "terraform"} {
		providerConfig, ok := cattleConfig[section].(map[string]any)
		if !ok {
			continue
		}
		providerValue, ok := providerConfig["provider"].(string)
		if !ok {
			continue
		}
		recognizedProvider := ""
		providerValue = strings.ToLower(providerValue)
		for _, name := range []string{AWSProvider, VsphereProvider, AzureProvider, HarvesterProvider, LinodeProvider, GoogleProvider} {
			if strings.Contains(providerValue, name) {
				recognizedProvider = name
				break
			}
		}
		if recognizedProvider == "" && (providerValue == DOProvider || strings.Contains(providerValue, "digitalocean")) {
			recognizedProvider = DOProvider
		}
		if recognizedProvider == "" {
			continue
		}
		if providerName != "" && providerName != recognizedProvider {
			return "", fmt.Errorf("clusterConfig and terraform providers differ: %s and %s this is currently not supported by the automation", providerName, recognizedProvider)
		}
		providerName = recognizedProvider
	}
	if providerName == VsphereProvider {
		return VsphereProvider, nil
	}
	return "", nil
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

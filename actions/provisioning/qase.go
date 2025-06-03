package provisioning

import (
	rancherEc2 "github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	upstream "go.qase.io/client"
)

// GetProvisioningSchemaParams gets a set of params from the cattle config and returns a qase params object
func GetProvisioningSchemaParams(client *rancher.Client, cattleConfig map[string]any) ([]upstream.Params, error) {
	var params []upstream.Params
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

	provider := CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

	osNames, err := provider.GetOSNamesFunc(client, credentialSpec, machineConfigSpec)
	if err != nil {
		return nil, err
	}

	osParam := upstream.Params{Title: "OS", Values: osNames}
	providerParam := upstream.Params{Title: "Provider", Values: []string{clusterConfig.Provider}}
	k8sParam := upstream.Params{Title: "K8sVersion", Values: []string{clusterConfig.KubernetesVersion}}
	cniParam := upstream.Params{Title: "CNI", Values: []string{clusterConfig.CNI}}

	params = append(params, providerParam, osParam, k8sParam, cniParam)

	return params, nil
}

// GetCustomSchemaParams gets a set of params from the cattle config and returns a qase params object
func GetCustomSchemaParams(client *rancher.Client, cattleConfig map[string]any) ([]upstream.Params, error) {
	var params []upstream.Params
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

	customConfig := new(rancherEc2.AWSEC2Configs)
	operations.LoadObjectFromMap(defaults.AWSEC2Configs, cattleConfig, customConfig)
	externalNodeProvider := ExternalNodeProviderSetup(clusterConfig.NodeProvider)

	osNames, err := externalNodeProvider.GetOSNamesFunc(client, *customConfig)
	if err != nil {
		return nil, err
	}

	osParam := upstream.Params{Title: "OS", Values: osNames}
	providerParam := upstream.Params{Title: "Provider", Values: []string{clusterConfig.Provider}}
	k8sParam := upstream.Params{Title: "K8sVersion", Values: []string{clusterConfig.KubernetesVersion}}
	cniParam := upstream.Params{Title: "CNI", Values: []string{clusterConfig.CNI}}

	params = append(params, providerParam, osParam, k8sParam, cniParam)

	return params, nil
}

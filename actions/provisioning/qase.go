package provisioning

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	upstream "go.qase.io/client"
)

func GetProvisioningSchemaParams(client *rancher.Client, cattleConfig map[string]any) ([]upstream.Params, error) {
	var params []upstream.Params
	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

	provider := CreateProvider(clusterConfig.Provider)
	credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
	machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

	osName, err := provider.GetOSNameFunc(client, credentialSpec, machineConfigSpec)
	if err != nil {
		return nil, err
	}

	osParam := upstream.Params{Title: "OS", Values: []string{osName}}
	providerParam := upstream.Params{Title: "Provider", Values: []string{clusterConfig.Provider}}
	k8sParam := upstream.Params{Title: "K8sVersion", Values: []string{clusterConfig.KubernetesVersion}}
	cniParam := upstream.Params{Title: "CNI", Values: []string{clusterConfig.CNI}}

	params = append(params, providerParam, osParam, k8sParam, cniParam)

	return params, nil
}

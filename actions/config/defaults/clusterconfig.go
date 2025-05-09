package defaults

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config/operations"
)

const (
	ClusterConfigKey = "clusterConfig"
	k8sVersionKey    = "kubernetesVersion"
)

// SetK8sDefault sets the k8s version to the latest version in the cattleConfig
func SetK8sDefault(client *rancher.Client, k8sType string, cattleConfig map[string]any) (map[string]any, error) {
	k8sKeyPath := []string{ClusterConfigKey, k8sVersionKey}
	k8sKeyValue, err := operations.GetValue(k8sKeyPath, cattleConfig)
	if err != nil {
		return nil, err
	}

	if k8sKeyValue == nil || k8sKeyValue == "" {
		versions, err := kubernetesversions.Default(client, k8sType, nil)
		if err != nil {
			return nil, err
		}

		cattleConfig, err = operations.ReplaceValue(k8sKeyPath, versions[0], cattleConfig)
		if err != nil {
			return nil, err
		}
	}

	return cattleConfig, nil
}

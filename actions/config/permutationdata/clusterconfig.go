package permutationdata

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/config/operations/permutations"
	"github.com/sirupsen/logrus"
)

const (
	clusterConfigKey = "clusterConfig"
	nodeProvidersKey = "nodeProvider"
	providerKey      = "provider"
	k8sVersionKey    = "kubernetesVersion"
	cniKey           = "cni"
)

// CreateK8sPermutation creates a permutation for k8s version and sets defaults
func CreateK8sPermutation(client *rancher.Client, k8sType string, config map[string]any) (*permutations.Permutation, error) {
	k8sKeyPath := []string{clusterConfigKey, k8sVersionKey}
	k8sKeyValue, err := operations.GetValue(k8sKeyPath, config)
	if err != nil {
		return nil, err
	}

	var k8sVersions []string
	if _, ok := k8sKeyValue.(string); ok {
		if k8sKeyValue.(string) == "" {
			k8sVersions, err = kubernetesversions.Default(client, k8sType, nil)
			if err != nil {
				return nil, err
			}
			k8sKeyValue = k8sVersions[0]
		}

		k8sPermutation := permutations.CreatePermutation(k8sKeyPath, []any{k8sKeyValue}, nil)
		return &k8sPermutation, nil
	}

	if len(k8sKeyValue.([]any)) < 1 || k8sKeyValue.([]any)[0] == "" {
		logrus.Warning("kubernetesVersion not set in config file")
		k8sKeyValue, err = kubernetesversions.Default(client, k8sType, nil)
		if err != nil {
			return nil, err
		}
	} else if k8sKeyValue.([]any)[0] == "all" {
		if k8sType == clusters.RKE2ClusterType.String() {
			k8sVersions, err = kubernetesversions.ListRKE2AllVersions(client)
			if err != nil {
				return nil, err
			}
		} else if k8sType == clusters.K3SClusterType.String() {
			k8sVersions, err = kubernetesversions.ListK3SAllVersions(client)
			if err != nil {
				return nil, err
			}
		}
	} else {
		k8sPermutation := permutations.CreatePermutation(k8sKeyPath, k8sKeyValue.([]any), nil)

		return &k8sPermutation, nil
	}

	var versions []any
	for _, version := range k8sVersions {
		versions = append(versions, version)
	}

	k8sPermutation := permutations.CreatePermutation(k8sKeyPath, versions, nil)

	return &k8sPermutation, nil
}

// CreateProviderPermutation creates a permutation for the provider
func CreateProviderPermutation(config map[string]any) (*permutations.Permutation, error) {
	providerKeyPath := []string{clusterConfigKey, providerKey}
	providerKeyValue, err := operations.GetValue(providerKeyPath, config)
	if err != nil {
		return nil, err
	}

	if _, ok := providerKeyValue.(string); ok {
		providerKeyValue = []any{providerKeyValue}
	}

	providerPermutation := permutations.CreatePermutation(providerKeyPath, providerKeyValue.([]any), nil)

	return &providerPermutation, nil
}

// CreateCNIPermutation creates a permutation for the CNI
func CreateCNIPermutation(config map[string]any) (*permutations.Permutation, error) {
	cniKeyPath := []string{clusterConfigKey, cniKey}
	cniKeyValue, err := operations.GetValue(cniKeyPath, config)
	if err != nil {
		return nil, err
	}

	if _, ok := cniKeyValue.(string); ok {
		cniKeyValue = []any{cniKeyValue}
	}

	cniPermutation := permutations.CreatePermutation(cniKeyPath, cniKeyValue.([]any), nil)

	return &cniPermutation, nil
}

package configmaps

import (
	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	coreV1 "k8s.io/api/core/v1"
)

// CreateConfigMap is a helper function that uses the wrangler context to create a config map on a namespace for a specific cluster.
func CreateConfigMap(client *rancher.Client, clusterID, namespace string, annotations, labels, data map[string]string) (*coreV1.ConfigMap, error) {
	configMapName := namegen.AppendRandomString("testcm")

	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	newConfigMap := NewConfigmapTemplate(configMapName, namespace, annotations, labels, data)
	configMap, err := clusterContext.Core.ConfigMap().Create(&newConfigMap)
	if err != nil {
		return nil, err
	}

	return configMap, nil
}

// CreateConfigMapWithTemplate creates a config map using the provided template, respecting its name and metadata.
func CreateConfigMapWithTemplate(client *rancher.Client, clusterID string, configMapTemplate *coreV1.ConfigMap) (*coreV1.ConfigMap, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	configMap, err := clusterContext.Core.ConfigMap().Create(configMapTemplate)
	if err != nil {
		return nil, err
	}

	return configMap, nil
}

package configmaps

import (
	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	clusterapi "github.com/rancher/tests/actions/kubeapi/clusters"
	coreV1 "k8s.io/api/core/v1"
	metaV1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NewConfigmapTemplate is a constructor that creates a configmap template
func NewConfigmapTemplate(configMapName, namespace string, annotations, labels, data map[string]string) coreV1.ConfigMap {
	if annotations == nil {
		annotations = make(map[string]string)
	}
	if labels == nil {
		labels = make(map[string]string)
	}
	if data == nil {
		data = make(map[string]string)
	}

	return coreV1.ConfigMap{
		ObjectMeta: metaV1.ObjectMeta{
			Name:        configMapName,
			Namespace:   namespace,
			Annotations: annotations,
			Labels:      labels,
		},
		Data: data,
	}
}

// CreateConfigMap is a helper function that uses the wrangler context to create a config map on a namespace for a specific cluster.
func CreateConfigMap(client *rancher.Client, clusterID, namespace string, annotations, labels, data map[string]string) (*coreV1.ConfigMap, error) {
	configMapName := namegen.AppendRandomString("testcm")

	wranglerCtx, err := clusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	newConfigMap := NewConfigmapTemplate(configMapName, namespace, annotations, labels, data)
	configMap, err := wranglerCtx.Core.ConfigMap().Create(&newConfigMap)
	if err != nil {
		return nil, err
	}

	return configMap, nil
}

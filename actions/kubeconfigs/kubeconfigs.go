package kubeconfigs

import (
	"fmt"

	extapi "github.com/rancher/rancher/pkg/apis/ext.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateKubeconfig creates a kubeconfig using public API
func CreateKubeconfig(client *rancher.Client, clusters []string, currentContext string, ttl *int64) (*extapi.Kubeconfig, error) {
	if len(clusters) == 0 {
		return nil, fmt.Errorf("clusters list must have at least one entry")
	}

	name := namegen.AppendRandomString("testkubeconfig")
	kubeconfig := &extapi.Kubeconfig{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: extapi.KubeconfigSpec{
			Clusters: clusters,
		},
	}

	if currentContext != "" {
		kubeconfig.Spec.CurrentContext = currentContext
	}
	if ttl != nil {
		kubeconfig.Spec.TTL = *ttl
	}

	createdKubeconfig, err := client.WranglerContext.Ext.Kubeconfig().Create(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create kubeconfig: %w", err)
	}

	return createdKubeconfig, nil
}

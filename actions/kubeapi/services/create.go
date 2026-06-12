package services

import (
	"github.com/rancher/shepherd/clients/rancher"
	extservicesapi "github.com/rancher/shepherd/extensions/kubeapi/services"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateService is a helper function that uses wrangler context to create a service in a namespace for a specific cluster.
func CreateService(client *rancher.Client, clusterID, serviceName, namespace string, spec corev1.ServiceSpec) (*corev1.Service, error) {
	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
		},
		Spec: spec,
	}

	createdService, err := extservicesapi.CreateServiceWithTemplate(client, clusterID, service)
	if err != nil {
		return nil, err
	}

	return createdService, nil
}

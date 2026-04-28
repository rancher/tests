package services

import (
	"github.com/rancher/shepherd/clients/rancher"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// CreateService is a helper function that uses wrangler context to create a service in a namespace for a specific cluster.
func CreateService(client *rancher.Client, clusterID, serviceName, namespace string, spec corev1.ServiceSpec) (*corev1.Service, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	service := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      serviceName,
			Namespace: namespace,
		},
		Spec: spec,
	}

	createdService, err := clusterContext.Core.Service().Create(service)
	if err != nil {
		return nil, err
	}

	return createdService, nil
}

// CreateServiceWithTemplate creates a service using the provided template, respecting its name and metadata.
func CreateServiceWithTemplate(client *rancher.Client, clusterID string, serviceTemplate *corev1.Service) (*corev1.Service, error) {
	clusterContext, err := extclusterapi.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	service, err := clusterContext.Core.Service().Create(serviceTemplate)
	if err != nil {
		return nil, err
	}

	return service, nil
}

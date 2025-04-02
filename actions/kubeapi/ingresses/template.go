package ingresses

import (
	"fmt"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/ingresses"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/services"
	corev1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
)

const (
	ServicePortName   = "port"
	ServicePortNumber = 80
	IngressHostName   = "sslip.io"
	IngressPath       = "/api"
)

// CreateServiceAndIngressTemplateForDeployment creates a service and an ingress template for a deployment
func CreateServiceAndIngressTemplateForDeployment(client *rancher.Client, clusterID, namespaceName string) (*networkingv1.Ingress, error) {
	serviceNameForDeployment := namegen.AppendRandomString("deploymentservice")

	serviceType := corev1.ServiceTypeNodePort
	ports := []corev1.ServicePort{
		{
			Name: ServicePortName,
			Port: ServicePortNumber,
		},
	}

	serviceTemplateForDeployment := services.NewServiceTemplate(serviceNameForDeployment, namespaceName, serviceType, ports, map[string]string{})

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, fmt.Errorf("failed to get steve client: %w", err)
	}

	_, err = services.CreateService(steveClient, serviceTemplateForDeployment)
	if err != nil {
		return nil, fmt.Errorf("failed to create service: %w", err)
	}

	pathTypePrefix := networkingv1.PathTypeImplementationSpecific
	paths := []networkingv1.HTTPIngressPath{
		{
			Path:     IngressPath,
			PathType: &pathTypePrefix,
			Backend: networkingv1.IngressBackend{
				Service: &networkingv1.IngressServiceBackend{
					Name: serviceNameForDeployment,
					Port: networkingv1.ServiceBackendPort{
						Number: ServicePortNumber,
					},
				},
			},
		},
	}

	ingressNameForDeployment := namegen.AppendRandomString("test-ingress")
	ingressTemplateForDeployment := ingresses.NewIngressTemplate(ingressNameForDeployment, namespaceName, fmt.Sprintf("%s.%s", ingressNameForDeployment, IngressHostName), paths)

	return &ingressTemplateForDeployment, nil
}

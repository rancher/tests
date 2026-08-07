package monitoring

import (
	"context"

	monitoringv1 "github.com/prometheus-operator/prometheus-operator/pkg/apis/monitoring/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/kubeapi/cluster"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

// CreateServiceMonitor creates a service monitor using Wrangler/Public API. The ServiceMonitor object follows the example on https://support.scc.suse.com/s/kb/Longhorn-monitoring-with-grafana-and-prometheus.
func CreateServiceMonitor(client *rancher.Client, clusterID string, name string, namespace string, serviceMonitorSpec monitoringv1.ServiceMonitorSpec) (*monitoringv1.ServiceMonitor, error) {
	wrangler, err := cluster.GetClusterWranglerContext(client, clusterID)
	if err != nil {
		return nil, err
	}

	serviceMonitorController, err := wrangler.ControllerFactory.ForKind(schema.GroupVersionKind{
		Group:   monitoringv1.SchemeGroupVersion.Group,
		Version: monitoringv1.SchemeGroupVersion.Version,
		Kind:    monitoringv1.ServiceMonitorsKind,
	})
	if err != nil {
		return nil, err
	}

	serviceMonitor := monitoringv1.ServiceMonitor{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
			Labels:    map[string]string{"name": name},
		},
		TypeMeta: metav1.TypeMeta{
			Kind:       monitoringv1.ServiceMonitorsKind,
			APIVersion: monitoringv1.SchemeGroupVersion.Group + "/" + monitoringv1.SchemeGroupVersion.Version,
		},
		Spec: serviceMonitorSpec,
	}

	err = serviceMonitorController.Client().Create(context.Background(), namespace, &serviceMonitor, &serviceMonitor, metav1.CreateOptions{})
	if err != nil {
		return nil, err
	}

	client.Session.RegisterCleanupFunc(func() error {
		return serviceMonitorController.Client().Delete(context.Background(), namespace, name, *metav1.NewDeleteOptions(60))
	})

	return &serviceMonitor, nil
}

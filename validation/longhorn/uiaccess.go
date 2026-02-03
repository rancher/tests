package longhorn

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	shepherdPods "github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/tests/actions/charts"
	corev1 "k8s.io/api/core/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	longhornFrontendServiceName = "longhorn-frontend"
)

// validateLonghornPods verifies that all pods in the longhorn-system namespace are in an active state
func validateLonghornPods(t *testing.T, client *rancher.Client, clusterID string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	t.Logf("Listing all pods in namespace %s", charts.LonghornNamespace)
	pods, err := steveClient.SteveType(shepherdPods.PodResourceSteveType).NamespacedSteveClient(charts.LonghornNamespace).List(nil)
	if err != nil {
		return fmt.Errorf("failed to list pods: %w", err)
	}

	if len(pods.Data) == 0 {
		return fmt.Errorf("no pods found in namespace %s", charts.LonghornNamespace)
	}

	t.Logf("Found %d pods in namespace %s", len(pods.Data), charts.LonghornNamespace)

	// Verify all pods are in running state
	for _, pod := range pods.Data {
		if pod.State.Name != "running" {
			return fmt.Errorf("pod %s is not in running state, current state: %s", pod.Name, pod.State.Name)
		}
	}

	t.Logf("All %d pods in namespace %s are in running state", len(pods.Data), charts.LonghornNamespace)
	return nil
}

// validateLonghornService verifies that the longhorn-frontend service is accessible and returns its URL
func validateLonghornService(t *testing.T, client *rancher.Client, clusterID string) (string, error) {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return "", fmt.Errorf("failed to get downstream client: %w", err)
	}

	t.Logf("Looking for service %s in namespace %s", longhornFrontendServiceName, charts.LonghornNamespace)

	// Wait for the service to be in active state
	var serviceResp *steveV1.SteveAPIObject
	err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.FiveMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		serviceID := fmt.Sprintf("%s/%s", charts.LonghornNamespace, longhornFrontendServiceName)
		serviceResp, err = steveClient.SteveType(stevetypes.Service).ByID(serviceID)
		if err != nil {
			return false, nil
		}

		if serviceResp.State.Name == "active" {
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return "", fmt.Errorf("service %s did not become active: %w", longhornFrontendServiceName, err)
	}

	t.Logf("Service %s is active", longhornFrontendServiceName)

	// Extract service information
	service := &corev1.Service{}
	err = steveV1.ConvertToK8sType(serviceResp.JSONResp, service)
	if err != nil {
		return "", fmt.Errorf("failed to convert service to k8s type: %w", err)
	}

	// Construct the service URL based on the service type
	var serviceURL string
	switch service.Spec.Type {
	case corev1.ServiceTypeClusterIP:
		// For ClusterIP, use the cluster IP and port
		if service.Spec.ClusterIP == "" {
			return "", fmt.Errorf("service %s has no cluster IP", longhornFrontendServiceName)
		}
		if len(service.Spec.Ports) == 0 {
			return "", fmt.Errorf("service %s has no ports defined", longhornFrontendServiceName)
		}
		serviceURL = fmt.Sprintf("http://%s.%s.svc.cluster.local:%d", longhornFrontendServiceName, charts.LonghornNamespace, service.Spec.Ports[0].Port)
		t.Logf("Service type is ClusterIP, URL: %s", serviceURL)

	case corev1.ServiceTypeNodePort:
		// For NodePort, we need to get a node IP
		if len(service.Spec.Ports) == 0 {
			return "", fmt.Errorf("service %s has no ports defined", longhornFrontendServiceName)
		}
		nodePort := service.Spec.Ports[0].NodePort
		if nodePort == 0 {
			return "", fmt.Errorf("service %s has no node port defined", longhornFrontendServiceName)
		}

		// Get a node IP
		nodes, err := steveClient.SteveType("node").List(nil)
		if err != nil || len(nodes.Data) == 0 {
			return "", fmt.Errorf("failed to get nodes: %w", err)
		}

		node := &corev1.Node{}
		err = steveV1.ConvertToK8sType(nodes.Data[0].JSONResp, node)
		if err != nil {
			return "", fmt.Errorf("failed to convert node to k8s type: %w", err)
		}

		// Get the node's internal IP
		var nodeIP string
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeInternalIP {
				nodeIP = addr.Address
				break
			}
		}

		if nodeIP == "" {
			return "", fmt.Errorf("could not find internal IP for node")
		}

		serviceURL = fmt.Sprintf("http://%s:%d", nodeIP, nodePort)
		t.Logf("Service type is NodePort, URL: %s", serviceURL)

	case corev1.ServiceTypeLoadBalancer:
		// For LoadBalancer, use the external IP
		if len(service.Status.LoadBalancer.Ingress) == 0 {
			return "", fmt.Errorf("service %s has no load balancer ingress", longhornFrontendServiceName)
		}
		if len(service.Spec.Ports) == 0 {
			return "", fmt.Errorf("service %s has no ports defined", longhornFrontendServiceName)
		}

		ingress := service.Status.LoadBalancer.Ingress[0]
		lbAddress := ingress.IP
		if lbAddress == "" {
			lbAddress = ingress.Hostname
		}
		serviceURL = fmt.Sprintf("http://%s:%d", lbAddress, service.Spec.Ports[0].Port)
		t.Logf("Service type is LoadBalancer, URL: %s", serviceURL)

	default:
		return "", fmt.Errorf("unsupported service type: %s", service.Spec.Type)
	}

	return serviceURL, nil
}

// validateLonghornStorageClassInRancher verifies that the Longhorn storage class exists and is accessible through Rancher API
func validateLonghornStorageClassInRancher(t *testing.T, client *rancher.Client, clusterID, storageClassName string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return fmt.Errorf("failed to get downstream client: %w", err)
	}

	t.Logf("Looking for storage class %s in Rancher", storageClassName)

	// Get the storage class
	storageClasses, err := steveClient.SteveType("storage.k8s.io.storageclass").List(nil)
	if err != nil {
		return fmt.Errorf("failed to list storage classes: %w", err)
	}

	for _, sc := range storageClasses.Data {
		if sc.Name == storageClassName {
			t.Logf("Found storage class %s in Rancher", storageClassName)
			return nil
		}
	}

	return fmt.Errorf("storage class %s not found in Rancher", storageClassName)
}

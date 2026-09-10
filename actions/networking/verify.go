package networking

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	kubeapinodes "github.com/rancher/shepherd/extensions/kubeapi/nodes"
	extdaemonsetapi "github.com/rancher/shepherd/extensions/kubeapi/workloads/daemonsets"
	"github.com/rancher/shepherd/extensions/kubectl"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/clusters"
	servicesapi "github.com/rancher/tests/actions/kubeapi/services"
	"github.com/rancher/tests/actions/services"
	"github.com/rancher/tests/actions/workloads"
	"github.com/rancher/tests/actions/workloads/daemonset"
	"github.com/sirupsen/logrus"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	nodeRole           = "control-plane"
	podHTTPPort        = 80
	serviceNamePrefix  = "test-service"
	connectivityPrefix = "connectivity-check-"
	hostPortNamePrefix = "host-port-connectivity-"
	nodePortNamePrefix = "node-port-connectivity-"
	proxyClientError   = "failed creating Proxy Client"
)

// VerifyPodConnectivity creates a daemonset and verifies its pods are reachable from control-plane nodes.
func VerifyPodConnectivity(client *rancher.Client, downstreamClient *v1.Client, clusterID, namespaceName, testName string, workloadConfigs *workloads.Workloads) error {
	if workloadConfigs == nil || workloadConfigs.DaemonSet == nil {
		return errors.New("daemonset workload config is required")
	}

	daemonSetConfig := workloadConfigs.DaemonSet.DeepCopy()
	if err := waitForDownstreamNamespace(client, clusterID, namespaceName); err != nil {
		return fmt.Errorf("failed waiting for namespace %s: %w", namespaceName, err)
	}

	daemonSetConfig.ObjectMeta.Namespace = namespaceName
	daemonSetConfig.ObjectMeta.GenerateName = strings.ToLower(testName)

	logrus.Info("Creating pod connectivity daemonset")
	testDaemonset, err := daemonset.CreateDaemonSetFromConfig(downstreamClient, clusterID, daemonSetConfig)
	if err != nil {
		return fmt.Errorf("failed to create connectivity daemonset: %w", err)
	}

	logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
	err = extdaemonsetapi.WaitForDaemonSetReady(client, clusterID, namespaceName, testDaemonset.Name)
	if err != nil {
		return fmt.Errorf("failed waiting for daemonset %s: %w", testDaemonset.Name, err)
	}

	logrus.Info("Verifying pod connectivity from control plane node")
	if err := VerifyNetworkPolicy(client, clusterID, namespaceName); err != nil {
		return fmt.Errorf("failed to verify pod connectivity: %w", err)
	}

	return nil
}

// VerifyHostPortConnectivity creates a daemonset and verifies its host port from worker nodes.
func VerifyHostPortConnectivity(client *rancher.Client, downstreamClient *v1.Client, clusterID, namespaceName string, hostPort int, endpoint string, workloadConfigs *workloads.Workloads) error {
	if workloadConfigs == nil || workloadConfigs.DaemonSet == nil || len(workloadConfigs.DaemonSet.Spec.Template.Spec.Containers) == 0 {
		return errors.New("daemonset workload config with a container is required")
	}

	daemonSetConfig := workloadConfigs.DaemonSet.DeepCopy()
	if err := waitForDownstreamNamespace(client, clusterID, namespaceName); err != nil {
		return fmt.Errorf("failed waiting for namespace %s: %w", namespaceName, err)
	}

	daemonSetConfig.ObjectMeta.Namespace = namespaceName
	daemonSetConfig.ObjectMeta.GenerateName = hostPortNamePrefix
	daemonSetConfig.Spec.Template.Spec.Containers[0].Ports = []corev1.ContainerPort{{
		HostPort:      int32(hostPort),
		ContainerPort: podHTTPPort,
		Protocol:      corev1.ProtocolTCP,
	}}

	logrus.Infof("Creating daemonset with name prefix: %s", daemonSetConfig.ObjectMeta.GenerateName)
	testDaemonset, err := daemonset.CreateDaemonSetFromConfig(downstreamClient, clusterID, daemonSetConfig)
	if err != nil {
		return fmt.Errorf("failed to create host port daemonset: %w", err)
	}

	logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
	if err := extdaemonsetapi.WaitForDaemonSetReady(client, clusterID, namespaceName, testDaemonset.Name); err != nil {
		return fmt.Errorf("failed waiting for daemonset %s: %w", testDaemonset.Name, err)
	}

	logrus.Infof("Verifying host port %d for daemonset %s", hostPort, testDaemonset.Name)
	if err := VerifyConnectivityFromWorkerNodes(client, clusterID, "localhost", hostPort, endpoint, expectedEndpointContent(endpoint, testDaemonset.Name)); err != nil {
		return fmt.Errorf("failed to verify host port connectivity: %w", err)
	}

	return nil
}

// VerifyNodePortConnectivity creates a daemonset and service and verifies the node port from worker nodes.
func VerifyNodePortConnectivity(client *rancher.Client, downstreamClient *v1.Client, clusterID, namespaceName string, nodePort int, endpoint string, workloadConfigs *workloads.Workloads) error {
	if workloadConfigs == nil || workloadConfigs.DaemonSet == nil {
		return errors.New("daemonset workload config is required")
	}

	daemonSetConfig := workloadConfigs.DaemonSet.DeepCopy()
	if err := waitForDownstreamNamespace(client, clusterID, namespaceName); err != nil {
		return fmt.Errorf("failed waiting for namespace %s: %w", namespaceName, err)
	}

	daemonSetConfig.ObjectMeta.Namespace = namespaceName
	daemonSetConfig.ObjectMeta.GenerateName = nodePortNamePrefix

	logrus.Infof("Creating daemonset with name prefix: %s", daemonSetConfig.ObjectMeta.GenerateName)
	testDaemonset, err := daemonset.CreateDaemonSetFromConfig(downstreamClient, clusterID, daemonSetConfig)
	if err != nil {
		return fmt.Errorf("failed to create node port daemonset: %w", err)
	}

	logrus.Infof("Verifying daemonset %s is running", testDaemonset.Name)
	if err := extdaemonsetapi.WaitForDaemonSetReady(client, clusterID, namespaceName, testDaemonset.Name); err != nil {
		return fmt.Errorf("failed waiting for daemonset %s: %w", testDaemonset.Name, err)
	}

	serviceName := namegen.AppendRandomString(serviceNamePrefix)
	logrus.Infof("Creating NodePort service %s on port %d", serviceName, nodePort)
	ports := []corev1.ServicePort{{
		Protocol: corev1.ProtocolTCP,
		Port:     podHTTPPort,
		NodePort: int32(nodePort),
	}}
	nodePortService := servicesapi.NewServiceTemplate(serviceName, namespaceName, corev1.ServiceTypeNodePort, ports, daemonSetConfig.Spec.Template.Labels)
	serviceResp, err := services.CreateService(downstreamClient, nodePortService)
	if err != nil {
		return fmt.Errorf("failed to create node port service: %w", err)
	}

	logrus.Infof("Verifying service %s is ready", serviceResp.Name)
	if err := services.VerifyService(downstreamClient, serviceResp); err != nil {
		return fmt.Errorf("failed to verify service %s: %w", serviceResp.Name, err)
	}

	logrus.Infof("Verifying node port %d for daemonset %s", nodePort, testDaemonset.Name)
	if err := VerifyConnectivityFromWorkerNodes(client, clusterID, "", nodePort, endpoint, expectedEndpointContent(endpoint, testDaemonset.Name)); err != nil {
		return fmt.Errorf("failed to verify node port connectivity: %w", err)
	}

	return nil
}

// VerifyNetworkPolicy verifies that pods are reachable from the node.
func VerifyNetworkPolicy(client *rancher.Client, clusterID string, namespaceName string) error {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	pods, err := steveclient.SteveType(stevetypes.Pod).NamespacedSteveClient(namespaceName).List(nil)
	if err != nil {
		return fmt.Errorf("failed to list pods in namespace %s: %w", namespaceName, err)
	}

	if len(pods.Data) == 0 {
		return fmt.Errorf("no pods found in namespace %s", namespaceName)
	}

	query, err := url.ParseQuery("labelSelector=node-role.kubernetes.io/" + nodeRole + "=true")
	if err != nil {
		return fmt.Errorf("failed to build node selector query: %w", err)
	}

	nodeList, err := steveclient.SteveType(stevetypes.Node).List(query)
	if err != nil {
		return fmt.Errorf("failed to list nodes with role %s: %w", nodeRole, err)
	}

	if len(nodeList.Data) == 0 {
		return fmt.Errorf("no nodes found with role %s", nodeRole)
	}

	for _, machine := range nodeList.Data {
		logrus.Info("Verifying pod connectivity from control plane node")
		for i := 0; i < len(pods.Data); i++ {
			podStatus := &corev1.PodStatus{}
			err = v1.ConvertToK8sType(pods.Data[i].Status, podStatus)
			if err != nil {
				return fmt.Errorf("failed to convert pod status for pod %s: %w", pods.Data[i].Name, err)
			}

			podIP := podStatus.PodIP
			if podIP == "" {
				return fmt.Errorf("pod %s in namespace %s has empty podIP", pods.Data[i].Name, namespaceName)
			}

			curlCommand := []string{"curl", "-fsS", "--connect-timeout", "10", fmt.Sprintf("http://%s:%d/", podIP, podHTTPPort)}
			excmdLog, err := executeCommandFromNode(client, clusterID, machine.Name, curlCommand)
			logrus.Debug(excmdLog)

			if err != nil {
				return fmt.Errorf("unable to connect to pod %s (%s) from machine %s: %w: %s", pods.Data[i].Name, podIP, machine.Name, err, excmdLog)
			}
		}
	}

	return nil
}

func expectedEndpointContent(endpoint, workloadName string) string {
	if endpoint == "/name.html" {
		return workloadName
	}

	return ""
}

func waitForDownstreamNamespace(client *rancher.Client, clusterID, namespaceName string) error {
	return kwait.PollUntilContextTimeout(context.Background(), 5*time.Second, time.Minute, true, func(ctx context.Context) (bool, error) {
		client, err := client.ReLogin()
		if err != nil {
			return false, err
		}

		dynamicClient, err := client.GetDownStreamClusterClient(clusterID)
		if err != nil {
			return false, err
		}

		_, err = dynamicClient.Resource(schema.GroupVersionResource{Version: "v1", Resource: "namespaces"}).Get(ctx, namespaceName, metav1.GetOptions{})
		if err != nil {
			if apierrors.IsNotFound(err) {
				return false, nil
			}

			return false, err
		}

		return true, nil
	})
}

// VerifyConnectivityFromWorkerNodes verifies if any worker node can access the provided IP, port, and endpoint.
// When expectedContent is set, the response must contain it. Each node's own IP is used if no address is provided.
func VerifyConnectivityFromWorkerNodes(client *rancher.Client, clusterID string, ip string, port int, endpoint, expectedContent string) error {
	if endpoint == "" || !strings.HasPrefix(endpoint, "/") {
		return fmt.Errorf("endpoint must start with '/': %q", endpoint)
	}

	query, err := url.ParseQuery(clusters.LabelWorker)
	if err != nil {
		return err
	}

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	nodeList, err := steveClient.SteveType(stevetypes.Node).List(query)
	if err != nil {
		return err
	}

	if len(nodeList.Data) == 0 {
		return errors.New("no worker nodes found")
	}

	for _, machine := range nodeList.Data {
		nodeIP := ip
		if nodeIP == "" {
			newNode := &corev1.Node{}
			err = v1.ConvertToK8sType(machine.JSONResp, newNode)
			if err != nil {
				return fmt.Errorf("failed to convert node %s: %w", machine.Name, err)
			}

			nodeIP = kubeapinodes.GetNodeIP(newNode, corev1.NodeExternalIP)
			if nodeIP == "" {
				nodeIP = kubeapinodes.GetNodeIP(newNode, corev1.NodeInternalIP)
			}
		}

		url := fmt.Sprintf("http://%s:%d%s", nodeIP, port, endpoint)
		logrus.Infof("Curling port %d from worker node", port)
		curlCommand := []string{"curl", "-fsS", "--connect-timeout", "10", url}
		log, err := executeCommandFromNode(client, clusterID, machine.Name, curlCommand)
		if err != nil {
			logrus.Infof("Curl failed on node %s: %v", machine.Name, err)
			continue
		}

		if expectedContent == "" || strings.Contains(log, expectedContent) {
			return nil
		} else {
			logrus.Infof("Curl result %s doesn't contain expected content '%s'", log, expectedContent)
		}
	}

	return fmt.Errorf("unable to connect to %s:%d%s from any worker node", ip, port, endpoint)
}

func executeCommandFromNode(client *rancher.Client, clusterID, nodeName string, command []string) (string, error) {
	overrides, err := json.Marshal(map[string]any{
		"spec": map[string]any{
			"hostNetwork": true,
			"nodeName":    nodeName,
			"tolerations": []map[string]string{{"operator": "Exists"}},
		},
	})
	if err != nil {
		return "", err
	}

	kubectlCommand := []string{
		"kubectl", "run", namegen.AppendRandomString(connectivityPrefix),
		"--image=curlimages/curl:8.12.1", "--restart=Never", "--rm", "--attach", "--quiet",
		"--overrides=" + string(overrides), "--command", "--",
	}
	kubectlCommand = append(kubectlCommand, command...)

	commandClient := client
	var commandLog string
	var commandErr error
	pollErr := kwait.PollUntilContextTimeout(context.Background(), 5*time.Second, 2*time.Minute, true, func(ctx context.Context) (bool, error) {
		commandLog, commandErr = kubectl.Command(commandClient, nil, clusterID, kubectlCommand, "")
		if commandErr == nil {
			return true, nil
		}
		if !strings.Contains(commandErr.Error(), proxyClientError) {
			return false, commandErr
		}

		logrus.Warnf("Downstream proxy creation failed for cluster %s: %v", clusterID, commandErr)
		refreshedClient, err := client.ReLogin()
		if err != nil {
			logrus.Warnf("Failed to refresh Rancher client for cluster %s: %v", clusterID, err)
			return false, nil
		}
		commandClient = refreshedClient

		return false, nil
	})
	if pollErr != nil {
		if commandErr != nil {
			return commandLog, fmt.Errorf("failed to execute connectivity command within two minutes: %w", commandErr)
		}
		return commandLog, pollErr
	}

	return commandLog, nil
}

func verifyConnectivityFromPod(client *rancher.Client, clusterID string, ip string, port int, workloadName string) error {
	execCmd := []string{"curl", "-s", fmt.Sprintf("%s:%d/name.html", ip, port)}
	log, err := kubectl.Command(client, nil, clusterID, execCmd, "")
	if err != nil {
		return err
	}

	if !strings.Contains(log, workloadName) { // This should be one of the pod's names.
		return fmt.Errorf("Curl result %s doesn't include the workload name %s", log, workloadName)
	}

	return nil
}

// VerifyLoadBalancerConnectivity verifies that the Load Balancer service is accessible by curling its IP:port.
func VerifyLoadBalancerConnectivity(client *rancher.Client, clusterID string, serviceID string, workloadName string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	service, err := steveClient.SteveType(stevetypes.Service).ByID(serviceID)
	if err != nil {
		return err
	}

	k8sService := &corev1.Service{}
	err = v1.ConvertToK8sType(service.JSONResp, k8sService)
	if err != nil {
		return err
	}

	if len(k8sService.Spec.Ports) == 0 {
		return fmt.Errorf("No ports (Spec.Ports) specified in service %s", k8sService.Name)
	}

	if len(k8sService.Status.LoadBalancer.Ingress) == 0 {
		return fmt.Errorf("No ingress (Status.LoadBalancer.Ingress) specified in service %s", k8sService.Name)
	}

	port := k8sService.Spec.Ports[0].Port
	ingress := k8sService.Status.LoadBalancer.Ingress[0]
	ip := ingress.IP
	if ip == "" {
		ip = ingress.Hostname
	}

	logrus.Infof("Testing connectivity with load balancer %s by curling %s:%d/name.html", k8sService.Name, ip, port)

	return verifyConnectivityFromPod(client, clusterID, ip, int(port), workloadName)
}

// VerifyClusterConnectivity verifies that the ClusterIP service is accessible from a worker node.
func VerifyClusterConnectivity(client *rancher.Client, clusterID string, serviceID string, port int, content string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	serviceResp, err := steveClient.SteveType(stevetypes.Service).ByID(serviceID)
	if err != nil {
		return err
	}

	newService := &corev1.Service{}
	err = v1.ConvertToK8sType(serviceResp.JSONResp, newService)
	if err != nil {
		return err
	}

	return VerifyConnectivityFromWorkerNodes(client, clusterID, newService.Spec.ClusterIP, port, "/name.html", content)
}

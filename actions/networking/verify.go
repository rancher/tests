package networking

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	kubeapinodes "github.com/rancher/shepherd/extensions/kubeapi/nodes"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/tests/actions/clusters"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
	corev1 "k8s.io/api/core/v1"
)

const (
	nodeRole       = "control-plane"
	pingCmd        = "ping -c 1"
	successfulPing = "0% packet loss"
)

// VerifyNetworkPolicy verifies that the network policy is working by pinging the pods from the node
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
		sshNode, err := sshkeys.GetSSHNodeFromMachine(client, &machine)
		if err != nil {
			return fmt.Errorf("failed to get ssh node for machine %s: %w", machine.Name, err)
		}

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

			pingExecCmd := pingCmd + " " + podIP
			excmdLog, err := sshNode.ExecuteCommand(pingExecCmd)
			logrus.Debug(excmdLog)

			if err != nil || !strings.Contains(excmdLog, successfulPing) {
				return fmt.Errorf("unable to ping pod %s (%s) from machine %s: %s", pods.Data[i].Name, podIP, machine.Name, excmdLog)
			}
		}
	}

	return nil
}

// verifyConnectivityFromWorkerNodes verifies if any worker node in the cluster is able to access the provided ip:port
// and retrieve the expected content from name.html.
func verifyConnectivityFromWorkerNodes(client *rancher.Client, clusterID string, ip string, port int, workloadName string) error {
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
		sshNode, err := sshkeys.GetSSHNodeFromMachine(client, &machine)
		if err != nil {
			logrus.Debugf("Could not SSH into worker node %s: %s", machine.Name, err.Error())
			continue
		}

		logrus.Debugf("Curling '%s:%d/name.html' from node %s", ip, port, machine.Name)
		log, err := sshNode.ExecuteCommand(fmt.Sprintf("curl -s %s:%d/name.html", ip, port))
		if err != nil && !errors.Is(err, &ssh.ExitMissingError{}) {
			logrus.Debugf("Curl failed on node %s: %v", machine.Name, err)
			continue
		}

		if strings.Contains(log, workloadName) { // This should be one of the pod's names.
			return nil
		} else {
			logrus.Debugf("Curl result %s doesn't contain expected content '%s'", log, workloadName)
		}
	}

	return fmt.Errorf("Unable to connect to %s:%d/name.html from any worker node", ip, port)
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

// VerifyNodePortConnectivity verifies that the node port is accessible by curling the worker node external IP
func VerifyNodePortConnectivity(client *rancher.Client, clusterID string, nodePort int, workloadName string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return err
	}

	query, err := url.ParseQuery(clusters.LabelWorker)
	if err != nil {
		return fmt.Errorf("failed to build worker node query: %w", err)
	}

	nodeList, err := steveClient.SteveType(stevetypes.Node).List(query)
	if err != nil {
		return fmt.Errorf("failed to list worker nodes: %w", err)
	}

	if len(nodeList.Data) == 0 {
		return errors.New("no worker nodes found")
	}

	for _, machine := range nodeList.Data {
		newNode := &corev1.Node{}
		err = v1.ConvertToK8sType(machine.JSONResp, newNode)
		if err != nil {
			return fmt.Errorf("failed to convert node %s: %w", machine.Name, err)
		}

		nodeIP := kubeapinodes.GetNodeIP(newNode, corev1.NodeExternalIP)
		if nodeIP == "" {
			nodeIP = kubeapinodes.GetNodeIP(newNode, corev1.NodeInternalIP)
		}

		logrus.Debugf("Curling node port %d on node %s (%s)", nodePort, machine.Name, nodeIP)
		verifyConnectivityFromPod(client, clusterID, nodeIP, nodePort, workloadName)
	}

	return fmt.Errorf("unable to access node port %d for workload %s", nodePort, workloadName)
}

// VerifyLoadBalancerConnectivity verifies that the Load Balancer service is accessible by curling its IP:port.
// This includes
func VerifyLoadBalancerConnectivity(t *testing.T, client *rancher.Client, clusterID string, serviceID string, workloadName string) {
	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	require.NoError(t, err)

	service, err := steveClient.SteveType(stevetypes.Service).ByID(serviceID)
	require.NoError(t, err)

	k8sService := &corev1.Service{}
	err = v1.ConvertToK8sType(service, k8sService)
	require.NoError(t, err)
	require.Equal(t, corev1.ServiceTypeLoadBalancer, k8sService.Spec.Type)
	require.NotEmpty(t, k8sService.Spec.Ports)
	require.NotEmpty(t, k8sService.Status.LoadBalancer.Ingress)

	port := k8sService.Spec.Ports[0].Port
	ip := k8sService.Status.LoadBalancer.Ingress[0].IP
	t.Logf("Testing connectivity with load balancer %s by curling %s:%d/name.html", k8sService.Name, ip, port)

	err = verifyConnectivityFromPod(client, clusterID, ip, int(port), workloadName)
	require.NoError(t, err)
}

// VerifyHostPortConnectivity verifies that the host port is accessible on worker nodes by SSHing directly into each node
func VerifyHostPortConnectivity(client *rancher.Client, clusterID string, hostPort int, workloadName string) error {
	return verifyConnectivityFromWorkerNodes(client, clusterID, "localhost", hostPort, workloadName)
}

// VerifyClusterConnectivity verifies that the ClusterIP service is accessible via SSH from a worker node
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

	return verifyConnectivityFromWorkerNodes(client, clusterID, newService.Spec.ClusterIP, port, content)
}

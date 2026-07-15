package networking

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	kubeapinodes "github.com/rancher/shepherd/extensions/kubeapi/nodes"
	"github.com/rancher/shepherd/extensions/kubectl"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/tests/actions/clusters"
	"github.com/sirupsen/logrus"
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
		execCmd := []string{"curl", fmt.Sprintf("%s:%s/name.html", nodeIP, strconv.Itoa(nodePort))}
		log, err := kubectl.Command(client, nil, clusterID, execCmd, "")
		if err != nil {
			return fmt.Errorf("curl command failed on node %s: %w", machine.Name, err)
		}

		if strings.Contains(log, workloadName) {
			return nil
		}
	}

	return fmt.Errorf("unable to access node port %d for workload %s", nodePort, workloadName)
}

// VerifyHostPortConnectivity verifies that the host port is accessible on worker nodes by SSHing directly into each node
func VerifyHostPortConnectivity(client *rancher.Client, clusterID string, hostPort int, workloadName string) error {
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
		sshNode, err := sshkeys.GetSSHNodeFromMachine(client, &machine)
		if err != nil {
			logrus.Debugf("Could not SSH into worker node %s, skipping: %v", machine.Name, err)
			continue
		}

		logrus.Debugf("Curling host port %d on worker node %s", hostPort, machine.Name)
		output, err := sshNode.ExecuteCommand(fmt.Sprintf("curl localhost:%d/name.html", hostPort))
		if err != nil {
			continue
		}

		if strings.Contains(output, workloadName) {
			return nil
		}
	}

	return fmt.Errorf("unable to access host port %d for workload %s on any worker node", hostPort, workloadName)
}

// VerifyClusterConnectivity verifies that the ClusterIP service is accessible via SSH from a worker node
func VerifyClusterConnectivity(client *rancher.Client, clusterID string, serviceID string, path string, content string) error {
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

	clusterIP := newService.Spec.ClusterIP

	query, err := url.ParseQuery(clusters.LabelWorker)
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
			logrus.Debugf("Could not SSH into worker node %s, trying next node: %v", machine.Name, err)
			continue
		}

		logrus.Debugf("Curling cluster IP %s:%s from node %s", clusterIP, path, machine.Name)
		log, err := sshNode.ExecuteCommand(fmt.Sprintf("curl %s:%s", clusterIP, path))
		if err != nil && !errors.Is(err, &ssh.ExitMissingError{}) {
			logrus.Debugf("Curl failed on node %s: %v, trying next node", machine.Name, err)
			continue
		}

		if strings.Contains(log, content) {
			return nil
		}
	}

	return fmt.Errorf("unable to connect to the cluster IP %s:%s from any worker node", clusterIP, path)
}

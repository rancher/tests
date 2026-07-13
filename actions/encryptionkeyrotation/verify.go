package encryptionkeyrotation

import (
	"fmt"
	"strings"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/sirupsen/logrus"
)

const (
	controlPlane              = "control-plane"
	nodeRoleControlPlaneLabel = "node-role.kubernetes.io/control-plane"
)

// VerifyEncryptionKeyRotation validates that encryption key rotation completed successfully on a cluster
func VerifyEncryptionKeyRotation(client *rancher.Client, clusterStatus *provv1.ClusterStatus, clusterType string) error {
	steveClient, err := client.Steve.ProxyDownstream(clusterStatus.ClusterName)
	if err != nil {
		return fmt.Errorf("failed to create downstream client for cluster %s: %w", clusterStatus.ClusterName, err)
	}

	nodeList, err := steveClient.SteveType(stevetypes.Node).List(nil)
	if err != nil {
		return fmt.Errorf("failed to list nodes for cluster %s: %w", clusterStatus.ClusterName, err)
	}

	if len(nodeList.Data) == 0 {
		return fmt.Errorf("no nodes found in cluster %s", clusterStatus.ClusterName)
	}

	controlPlaneCount := 0

	for _, machine := range nodeList.Data {
		if !isControlPlaneMachine(machine.Labels) {
			continue
		}

		controlPlaneCount++
		logrus.Debugf("Selected control-plane node: %s", machine.Name)

		sshNode, err := sshkeys.GetSSHNodeFromMachine(client, &machine)
		if err != nil {
			return fmt.Errorf("failed to get ssh node for machine %s: %w", machine.Name, err)
		}

		logrus.Debugf("Connecting to control plane node: %s", machine.Name)

		output, err := sshNode.ExecuteCommand(fmt.Sprintf("sudo %s secrets-encrypt status", clusterType))
		if err != nil {
			return fmt.Errorf("failed to run secrets-encrypt status command on node %s: %w, output: %s", machine.Name, err, output)
		}

		statusOutput := output
		logrus.Debugf("Encryption key rotation status output:\n%s", statusOutput)

		if strings.Contains(statusOutput, "Current Rotation Stage") && strings.Contains(statusOutput, "rotate") && !strings.Contains(statusOutput, "reencrypt_finished") {
			return fmt.Errorf("encryption key rotation is not finished yet: %s", statusOutput)
		}

		if !strings.Contains(statusOutput, "reencrypt_finished") {
			return fmt.Errorf("expected 'reencrypt_finished' in output, but got: %s", statusOutput)
		}

		if !strings.Contains(statusOutput, "All hashes match") {
			return fmt.Errorf("expected 'All hashes match' in output, but got: %s", statusOutput)
		}
	}

	if controlPlaneCount == 0 {
		return fmt.Errorf("no nodes found with control-plane role in cluster %s", clusterStatus.ClusterName)
	}

	return nil
}

func isControlPlaneMachine(labels map[string]string) bool {
	if labels == nil {
		return false
	}

	return strings.EqualFold(labels[nodeRoleControlPlaneLabel], "true")
}

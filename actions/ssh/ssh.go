package ssh

import (
	"errors"
	"net/url"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/shepherd/pkg/nodes"
	"github.com/rancher/tests/actions/clusters"
	"github.com/sirupsen/logrus"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionsClusters "github.com/rancher/shepherd/extensions/clusters"
)

const (
	nodeListEmptyMessageError = "node list is empty"
)

// CreateSSHNode is a helper to create a SSH Node
func CreateSSHNode(client *rancher.Client, clusterName string, clusterID string) (*nodes.Node, error) {

	steveClient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, err
	}

	provisioningClusterID, err := extensionsClusters.GetV1ProvisioningClusterByName(client, clusterName)
	if err != nil {
		return nil, err
	}

	cluster, err := client.Steve.SteveType(extensionsClusters.ProvisioningSteveResourceType).ByID(provisioningClusterID)
	if err != nil {
		return nil, err
	}

	newCluster := &provv1.Cluster{}
	err = steveV1.ConvertToK8sType(cluster, newCluster)
	if err != nil {
		return nil, err
	}

	sshNode := &nodes.Node{}

	logrus.Infof("Getting the node using the label [%v]", clusters.LabelWorker)
	query, err := url.ParseQuery(clusters.LabelWorker)
	if err != nil {
		return nil, err
	}

	nodeList, err := steveClient.SteveType("node").List(query)
	if err != nil {
		return nil, err
	}

	if len(nodeList.Data) == 0 {
		return nil, errors.New(nodeListEmptyMessageError)
	}

	firstMachine := nodeList.Data[0]

	sshNode, err = sshkeys.GetSSHNodeFromMachine(client, &firstMachine)
	if err != nil {
		return nil, err
	}

	return sshNode, nil
}

// RunLocalCommand is a helper to run a local command and return the output
func RunLocalCommand(cmd string) (string, error) {
	c := exec.Command("sh", "-c", cmd)
	output, err := c.CombinedOutput()

	return string(output), err
}

// GetNodeSSHKeyPath is a helper to get the SSH key path for a node based on the pool role and the EC2 configs
func GetNodeSSHKeyPath(poolRole string, ec2Configs *ec2.AWSEC2Configs) string {
	sshPath := nodes.GetSSHPath().SSHPath

	if strings.Contains(poolRole, "windows") {
		for _, cfg := range ec2Configs.AWSEC2Config {
			for _, role := range cfg.Roles {
				if strings.Contains(role, "windows") && cfg.AWSSSHKeyName != "" {
					return filepath.Join(sshPath, cfg.AWSSSHKeyName)
				}
			}
		}
	} else {
		for _, cfg := range ec2Configs.AWSEC2Config {
			isWindows := false
			for _, role := range cfg.Roles {
				if strings.Contains(role, "windows") {
					isWindows = true
					break
				}
			}

			if !isWindows && cfg.AWSSSHKeyName != "" {
				return filepath.Join(sshPath, cfg.AWSSSHKeyName)
			}
		}
	}

	return ""
}

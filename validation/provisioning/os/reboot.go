//go:build os

package os

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/sshkeys"
	nodeec2 "github.com/rancher/tests/actions/nodes/ec2"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

func rebootNodeRoles(client *rancher.Client, cluster *steveV1.SteveAPIObject) error {
	client, err := client.ReLogin()
	if err != nil {
		return err
	}

	clusterID, err := clusters.GetClusterIDByName(client, cluster.Name)
	if err != nil {
		return err
	}

	roles := []string{"etcd", "control-plane", "worker"}

	for _, role := range roles {
		query, err := url.ParseQuery("labelSelector=node-role.kubernetes.io/" + role + "=true")
		if err != nil {
			return err
		}

		nodes, err := listNodesByRole(client, clusterID, query)
		if err != nil {
			return fmt.Errorf("failed to list %s nodes: %w", role, err)
		}

		var selectedNode *steveV1.SteveAPIObject
		for index := range nodes.Data {
			node := &nodes.Data[index]
			if hasExclusiveRole(node, role, roles) {
				selectedNode = node
				break
			}
		}
		if selectedNode == nil {
			return fmt.Errorf("no dedicated node found for %s role", role)
		}

		sshNode, err := sshkeys.GetSSHNodeFromMachine(client, selectedNode)
		if err != nil {
			return fmt.Errorf("failed to get SSH credentials for %s node %s: %w", role, selectedNode.Name, err)
		}

		logrus.Infof("Rebooting node %s on cluster %s", selectedNode.Name, cluster.Name)
		if err := nodeec2.RebootNode(client, *sshNode, cluster.ID, clusterID); err != nil {
			return fmt.Errorf("failed to reboot %s node %s: %w", role, selectedNode.Name, err)
		}
	}

	return nil
}

func hasExclusiveRole(node *steveV1.SteveAPIObject, role string, roles []string) bool {
	for _, nodeRole := range roles {
		hasRole := node.Labels["node-role.kubernetes.io/"+nodeRole] == "true"
		if nodeRole == role && !hasRole {
			return false
		}
		if nodeRole != role && hasRole {
			return false
		}
	}

	return true
}

func listNodesByRole(client *rancher.Client, clusterID string, query url.Values) (*steveV1.SteveCollection, error) {
	var nodes *steveV1.SteveCollection
	var lastErr error

	err := kwait.PollUntilContextTimeout(context.TODO(), 10*time.Second, 5*time.Minute, true, func(ctx context.Context) (bool, error) {
		downstreamClient, err := client.Steve.ProxyDownstream(clusterID)
		if err != nil {
			return false, err
		}

		nodes, err = downstreamClient.SteveType(stevetypes.Node).List(query)
		if err != nil {
			if strings.Contains(err.Error(), "tunnel disconnect") || strings.Contains(err.Error(), "Unknown schema type [node]") {
				lastErr = err
				return false, nil
			}

			return false, err
		}

		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return nil, lastErr
		}

		return nil, err
	}

	return nodes, nil
}

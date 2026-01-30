package scalinginput

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/rancher/norman/types"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	nodestat "github.com/rancher/shepherd/extensions/nodes"
	"github.com/rancher/shepherd/extensions/sshkeys"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/sirupsen/logrus"
	"golang.org/x/crypto/ssh"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	shutdownCommand = "sudo shutdown -h now"
	controlPlane    = "control-plane"
	etcd            = "etcd"
	worker          = "worker"

	unreachableCondition   = "NodeStatusUnknown"
	machineSteveAnnotation = "cluster.x-k8s.io/machine"
	etcdLabel              = "node-role.kubernetes.io/etcd"
	clusterLabel           = "cluster.x-k8s.io/cluster-name"
)

// MatchNodeToRole returns the count and list of nodes that match the specified role(s) in a given cluster. Error returned, if any.
func MatchNodeToRole(client *rancher.Client, clusterID string, nodeRoles *machinepools.NodeRoles) ([]management.Node, error) {
	machines, err := client.Management.Node.List(&types.ListOpts{Filters: map[string]interface{}{
		"clusterId": clusterID,
	}})
	if err != nil {
		return nil, err
	}

	matchingNodes := []management.Node{}

	for _, machine := range machines.Data {
		if machine.Etcd == nodeRoles.Etcd && machine.ControlPlane == nodeRoles.ControlPlane && machine.Worker == nodeRoles.Worker {
			matchingNodes = append(matchingNodes, machine)
		}
	}
	if len(matchingNodes) == 0 {
		return nil, errors.New("matching node name is empty")
	}

	return matchingNodes, err
}

// ReplaceNodes replaces the last node with the specified role(s) in a k3s/rke2 cluster
func ReplaceNodes(client *rancher.Client, clusterName string, nodeRoles machinepools.NodeRoles) error {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	nodesToDelete, err := MatchNodeToRole(client, clusterID, &nodeRoles)
	if err != nil {
		return err
	}

	for i := range nodesToDelete {
		machineToDelete, err := client.Steve.SteveType(stevetypes.Machine).ByID("fleet-default/" + nodesToDelete[i].Annotations[machineSteveAnnotation])
		if err != nil {
			return err
		}

		logrus.Infof("Deleting node: %s", nodesToDelete[i].NodeName)
		err = client.Steve.SteveType(stevetypes.Machine).Delete(machineToDelete)
		if err != nil {
			return err
		}

		err = kwait.PollUntilContextTimeout(context.TODO(), 500*time.Millisecond, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
			_, err = client.Steve.SteveType(stevetypes.Machine).ByID(machineToDelete.ID)
			if err != nil {
				logrus.Infof("Node has successfully been deleted!")
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return err
		}

		logrus.Info("Waiting for node to be replaced")
		err = kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.ThirtyMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
			err = clusters.WaitClusterToBeUpgraded(client, clusterID)
			if err != nil {
				return false, nil
			}

			err = nodestat.AllMachineReady(client, clusterID, defaults.ThirtyMinuteTimeout)
			if err != nil {
				return false, nil
			}

			return true, nil
		})
		if err != nil {
			return err
		}
	}

	return nil
}

// ReplaceRKE1Nodes replaces the last node with the specified role(s) in a rke1 cluster
func ReplaceRKE1Nodes(client *rancher.Client, clusterName string, isEtcd bool, isControlPlane bool, isWorker bool) error {
	clusterID, err := clusters.GetClusterIDByName(client, clusterName)
	if err != nil {
		return err
	}

	var pool machinepools.NodeRoles
	pool.Etcd = isEtcd
	pool.ControlPlane = isControlPlane
	pool.Worker = isWorker

	nodesToDelete, err := MatchNodeToRole(client, clusterID, &pool)
	if err != nil {
		return err
	}

	for i := range nodesToDelete {
		logrus.Info("Deleting node: " + nodesToDelete[i].NodeName)
		err = client.Management.Node.Delete(&nodesToDelete[i])
		if err != nil {
			return err
		}

		err = nodestat.IsNodeDeleted(client, nodesToDelete[i].NodeName, clusterID)
		if err != nil {
			return err
		}

		err = nodestat.AllManagementNodeReady(client, clusterID, defaults.ThirtyMinuteTimeout)
		if err != nil {
			return err
		}
	}

	return nil
}

// shutdownFirstNodeWithRole uses ssh to shutdown the first node matching the specified role in a given cluster.
func shutdownFirstNodeWithRole(client *rancher.Client, clusterID, nodeRole string) (*steveV1.SteveAPIObject, error) {
	steveclient, err := client.Steve.ProxyDownstream(clusterID)
	if err != nil {
		return nil, err
	}

	query, err := url.ParseQuery("labelSelector=node-role.kubernetes.io/" + nodeRole + "=true")
	if err != nil {
		return nil, err
	}

	nodeList, err := steveclient.SteveType(stevetypes.Node).List(query)
	if err != nil {
		return nil, err
	}

	firstMachine := nodeList.Data[0]

	sshNode, err := sshkeys.GetSSHNodeFromMachine(client, &firstMachine)
	if err != nil {
		return nil, err
	}

	logrus.Infof("Running node auto-replace on node %s", firstMachine.Name)

	// Shutdown node using ssh outside of Rancher to simulate unhealthy node
	_, err = sshNode.ExecuteCommand(shutdownCommand)
	if err != nil && !errors.Is(err, &ssh.ExitMissingError{}) {
		return nil, err
	}

	return &firstMachine, nil
}

// matchNodeToMachinePool takes a given node name and returns the cluster's first matching machinePool from its RKEConfig, if any.
func matchNodeToMachinePool(clusterObject *steveV1.SteveAPIObject, nodeName string) (*provv1.RKEMachinePool, error) {
	clusterSpec := &provv1.ClusterSpec{}
	err := steveV1.ConvertToK8sType(clusterObject.Spec, clusterSpec)
	if err != nil {
		return nil, err
	}

	for _, pool := range clusterSpec.RKEConfig.MachinePools {
		if strings.Contains(nodeName, "-"+pool.Name+"-") {

			return &pool, nil
		}
	}

	return nil, errors.New("could not find matching machine pool for this node")
}

package machines

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/rancher/norman/types"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	nodestat "github.com/rancher/shepherd/extensions/nodes"
	"github.com/sirupsen/logrus"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	machineAnnotation = "cluster.x-k8s.io/machine"
	runningState      = "running"
)

// VerifyMachineReplacement verifies that a machine has been successfully replaced by checking for its deletion.
func VerifyMachineReplacement(client *rancher.Client, replacedMachine *v1.SteveAPIObject) error {
	err := kwait.PollUntilContextTimeout(context.TODO(), 5*time.Second, defaults.TenMinuteTimeout, true, func(ctx context.Context) (done bool, err error) {
		_, err = client.Steve.SteveType(stevetypes.Machine).ByID(replacedMachine.ID)
		if err != nil {
			logrus.Infof("Node has successfully been deleted!")
			return true, nil
		}

		return false, nil
	})

	if err != nil {
		return fmt.Errorf("error while waiting for machine to be deleted: %w", err)
	}

	return nil
}

// VerifyMachinesReady polls until every machine in the cluster is running.
func VerifyMachinesReady(client *rancher.Client, clusterID string, timeout time.Duration) error {
	var lastErr error

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	err := kwait.PollUntilContextTimeout(ctx, nodestat.PollInterval, timeout, true, func(context.Context) (bool, error) {
		nodes, err := client.Management.Node.List(&types.ListOpts{Filters: map[string]any{
			"clusterId": clusterID,
		}})
		if err != nil {
			return false, nil
		}

		var notReady []string
		for _, node := range nodes.Data {
			machineName := node.Annotations[machineAnnotation]

			machine, err := client.Steve.SteveType(stevetypes.Machine).ByID(namespaces.FleetDefault + "/" + machineName)
			if err != nil {
				notReady = append(notReady, fmt.Sprintf("%s (node %s): lookup failed: %v", machineName, node.NodeName, err))
				continue
			}

			switch {
			case machine.State == nil:
				notReady = append(notReady, fmt.Sprintf("%s (node %s): state is nil", machineName, node.NodeName))
			case machine.State.Error:
				notReady = append(notReady, fmt.Sprintf("%s (node %s): error state %q: %s", machineName, node.NodeName, machine.State.Name, machine.State.Message))
			case machine.State.Name != runningState:
				notReady = append(notReady, fmt.Sprintf("%s (node %s): state %q: %s", machineName, node.NodeName, machine.State.Name, machine.State.Message))
			}
		}

		if len(notReady) > 0 {
			lastErr = fmt.Errorf("%d of %d machines not running: %s", len(notReady), len(nodes.Data), strings.Join(notReady, "; "))
			return false, nil
		}

		return true, nil
	})
	if err != nil {
		if lastErr != nil {
			return fmt.Errorf("%w: last observed: %v", err, lastErr)
		}

		return err
	}

	return nil
}

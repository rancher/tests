package machines

import (
	"fmt"
	"net/url"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/machinepools"
)

const (
	etcdLabel    = "rke.cattle.io/etcd-role"
	controlLabel = "rke.cattle.io/control-plane-role"
	workerLabel  = "rke.cattle.io/worker-role"
)

// GetMachinesByRole retrieves all machines from a cluster based on the specified role.
// Returns a slice of machines with the matching role label.
func GetMachinesByRole(client *rancher.Client, cluster *v1.SteveAPIObject, role machinepools.NodeRoles) ([]v1.SteveAPIObject, error) {
	query := url.Values{}
	query.Add("filter", fmt.Sprintf("spec.clusterName=%s", cluster.Name))

	machines, err := client.Steve.SteveType(stevetypes.Machine).List(query)
	if err != nil {
		return nil, fmt.Errorf("failed to list machines: %w", err)
	}

	var matchingMachines []v1.SteveAPIObject
	for _, machine := range machines.Data {
		labels := machine.ObjectMeta.Labels
		if labels == nil {
			continue
		}

		match := true
		if role.Etcd && labels[etcdLabel] != "true" {
			match = false
		}
		if role.ControlPlane && labels[controlLabel] != "true" {
			match = false
		}
		if role.Worker && labels[workerLabel] != "true" {
			match = false
		}

		if match {
			matchingMachines = append(matchingMachines, machine)
		}
	}

	return matchingMachines, nil
}

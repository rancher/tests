package rke2k3s

import (
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/steve"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/sirupsen/logrus"
)

// DeleteInitMachine deletes the init machine from the specified cluster.
func DeleteInitMachine(client *rancher.Client, clusterID string) error {
	initMachine, err := machinepools.GetInitMachine(client, clusterID)
	if err != nil {
		return err
	}

	err = client.Steve.SteveType(stevetypes.Machine).Delete(initMachine)
	if err != nil {
		return err
	}

	logrus.Debugf("Waiting for the init machine to be deleted on cluster (%s)", clusterID)
	err = steve.WaitForResourceDeletion(client.Steve, initMachine, defaults.FiveHundredMillisecondTimeout, defaults.TenMinuteTimeout)
	if err != nil {
		return err
	}

	logrus.Debugf("Waiting for the init machine to be replaced on cluster (%s)", clusterID)
	err = clusters.WatchAndWaitForCluster(client, clusterID)
	if err != nil {
		return err
	}

	return nil
}

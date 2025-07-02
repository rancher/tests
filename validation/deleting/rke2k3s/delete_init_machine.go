package rke2k3s

import (
	"github.com/rancher/rancher/tests/v2/actions/machinepools"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/steve"
	"github.com/sirupsen/logrus"
)

// deleteInitMachine deletes the init machine from the specified cluster.
func deleteInitMachine(client *rancher.Client, clusterID string) error {
	initMachine, err := machinepools.GetInitMachine(client, clusterID)
	if err != nil {
		return err
	}

	err = client.Steve.SteveType(stevetypes.Machine).Delete(initMachine)
	if err != nil {
		return err
	}

	logrus.Info("Awaiting machine deletion...")
	err = steve.WaitForResourceDeletion(client.Steve, initMachine, defaults.FiveHundredMillisecondTimeout, defaults.TenMinuteTimeout)
	if err != nil {
		return err
	}

	logrus.Info("Awaiting machine replacement...")
	err = clusters.WatchAndWaitForCluster(client, clusterID)
	if err != nil {
		return err
	}

	return nil
}

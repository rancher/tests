//go:build (validation || recurring || ipv6 || dualstack || infra.rke2k3s) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !stress && !sanity && !extended

package k3s

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/machines"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/deleting"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestDeleteMachine(t *testing.T) {
	t.Parallel()

	d := deleting.Setup(t, defaults.K3S, false)

	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd: true,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker: true,
	}

	tests := []struct {
		name      string
		nodeRoles machinepools.NodeRoles
		cluster   *v1.SteveAPIObject
	}{
		{"K3S_Replace_Control_Plane", nodeRolesControlPlane, d.Cluster},
		{"K3S_Replace_ETCD", nodeRolesEtcd, d.Cluster},
		{"K3S_Replace_Worker", nodeRolesWorker, d.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			d.Session.Cleanup()
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			machineList, err := machines.GetMachinesByRole(d.Client, tt.cluster, tt.nodeRoles)
			require.NoError(t, err)

			machineToDelete := machineList[0]
			logrus.Infof("Deleting machine (%s) from cluster (%s)", machineToDelete.Name, tt.cluster.Name)
			err = d.Client.Steve.SteveType(stevetypes.Machine).Delete(&machineToDelete)
			require.NoError(t, err)

			err = machines.VerifyMachineReplacement(d.Client, &machineToDelete)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster is ready after machine replacement (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(d.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(d.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(d.Client, tt.cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(d.Client, d.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

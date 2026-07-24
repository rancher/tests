//go:build validation || capi

package k3s

import (
	"testing"

	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	capi "github.com/rancher/tests/actions/capipnp"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/provisioning/capipnp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestProvisioning(t *testing.T) {
	t.Parallel()

	k := capipnp.Setup(t, "k3s")

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
	}{
		{"CAPI_K3S|etcd_cp_worker", nodeRolesAll},
		{"CAPI_K3S|etcd_cp|worker", nodeRolesShared},
		{"CAPI_K3S|etcd|cp|worker", nodeRolesDedicated},
		{"CAPI_K3S|3_etcd|2_cp|3_worker", nodeRolesStandard},
	}

	for _, tt := range tests {
		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterName := namegen.AppendRandomString(k.CapiConfig.ClusterNamePrefix)

			clusterManifest, err := capi.CreateClusterManifestFromDefaults(k.CapiConfig, tt.machinePools, clusterName)
			require.NoError(t, err)

			t.Cleanup(func() {
				logrus.Infof("Cleaning up cluster (%s)", clusterName)
				cleanupErr := capi.DeleteCAPICluster(k.Client, clusterName, clusterManifest)
				require.NoError(t, cleanupErr)
			})

			logrus.Infof("Creating cluster: (%s)", clusterName)
			err = capi.CreateCAPICluster(k.StandardUserClient, clusterName, clusterManifest)
			require.NoError(t, err)

			cluster, err := clusters.GetClusterByName(k.Client, clusterName)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(k.Client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(k.Client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(k.Client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(k.Client, cluster.Name)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(k.Client, k.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

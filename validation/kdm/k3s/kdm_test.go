//go:build kdm

package kdm

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	kdmTest "github.com/rancher/tests/validation/kdm"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestKDM(t *testing.T) {
	t.Parallel()

	k := kdmTest.Setup(t, defaults.K3S)

	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
	}{
		{"K3S_KDM", nodeRolesStandard, k.StandardUserClient},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.Session.Cleanup()
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.CattleConfig, clusterConfig)
			clusterConfig.MachinePools = tt.machinePools

			require.NotNil(t, clusterConfig.Provider)

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := provider.LoadMachineConfigFunc(k.CattleConfig)

			logrus.Infof("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(tt.client, cluster.Name)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(k.Client, k.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

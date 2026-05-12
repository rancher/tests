//go:build validation || (recurring && registries) || registries

package registries

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestAuthenticatedRegistry(t *testing.T) {
	r := registriesSetup(t)

	nodeRolesAll := []provisioninginput.MachinePools{
		provisioninginput.AllRolesMachinePool,
	}

	nodeRolesDedicated := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	tests := []struct {
		name                  string
		client                *rancher.Client
		clusterType           string
		machinePools          []provisioninginput.MachinePools
		systemDefaultRegistry string
	}{
		{"Auth_RKE2", r.standardUserClient, defaults.RKE2, nodeRolesDedicated, r.terraformConfig.StandaloneRegistry.AuthRegistryFQDN},
		{"Auth_K3S", r.standardUserClient, defaults.K3S, nodeRolesAll, r.terraformConfig.StandaloneRegistry.AuthRegistryFQDN},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

			clusterConfig.Registries.RKE2Registries.Configs[tt.systemDefaultRegistry] = clusterConfig.Registries.RKE2Registries.Configs["<required>"]
			delete(clusterConfig.Registries.RKE2Registries.Configs, "<required>")

			(*clusterConfig.Advanced.MachineSelectors)[0].Config.Data["system-default-registry"] = tt.systemDefaultRegistry
			clusterConfig.Registries.RKE2Password = r.terraformConfig.StandaloneRegistry.RegistryPassword
			clusterConfig.Registries.RKE2Username = r.terraformConfig.StandaloneRegistry.RegistryUsername
			clusterConfig.MachinePools = tt.machinePools

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

			logrus.Infof("Provisioning %s cluster", tt.clusterType)
			cluster, err := resources.ProvisionRKE2K3SCluster(t, r.client, tt.clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(r.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(r.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(r.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

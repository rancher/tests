//go:build validation || (recurring && registries) || registries

package registries

import (
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestNonAuthenticatedRegistryWindows(t *testing.T) {
	var err error
	r := registriesSetup(t)

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	nodeRolesDedicatedWindows := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
		provisioninginput.WindowsMachinePool,
	}

	tests := []struct {
		name                  string
		client                *rancher.Client
		clusterType           string
		machinePools          []provisioninginput.MachinePools
		systemDefaultRegistry string
	}{
		{"Non_Auth_RKE2_Windows", r.standardUserClient, defaults.RKE2, nodeRolesDedicatedWindows, r.terraformConfig.StandaloneRegistry.NonAuthRegistryFQDN},
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
			clusterConfig.MachinePools = tt.machinePools

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)
			windowsMachineConfigs := externalNodeProvider.GetWindowsPoolsFunc(tt.client, *awsEC2Configs)
			if len(windowsMachineConfigs) == 0 {
				t.Skip("Windows test requires a windows machine pool")
			}

			logrus.Infof("Provisioning %s cluster", tt.clusterType)
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
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

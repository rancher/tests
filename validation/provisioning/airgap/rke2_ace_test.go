//go:build validation || (recurring && airgap) || airgap

package airgap

import (
	"testing"

	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/registries"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestAirgapRKE2ACE(t *testing.T) {
	r := airgapSetup(t, defaults.RKE2)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 1
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 1
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 1

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
	}{
		{"RKE2_Airgap_ACE", r.standardUserClient, nodeRolesStandard},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()

			if r.tunnel != nil {
				r.tunnel.StopBastionSSHTunnel()
			}
		})

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningAirgapCustomCluster(tt.client, clusterConfig, &externalNodeProvider, awsEC2Configs, r.terraformConfig.AirgapBastion)
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

			clusterStatus := &provv1.ClusterStatus{}
			err = steveV1.ConvertToK8sType(cluster.Status, clusterStatus)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods use private registry")
			_, err = registries.CheckAllClusterPodsForRegistryPrefix(tt.client, clusterStatus.ClusterName, r.terraformConfig.PrivateRegistries.SystemDefaultRegistry)
			require.NoError(t, err)

			provisioning.VerifyACEAirgap(t, tt.client, cluster)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

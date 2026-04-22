//go:build validation || prime

package autoscaling

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/scaling"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestAutoScalingKDM(t *testing.T) {
	s := autoScalingSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	rke2Versions, err := kubernetesversions.ListRKE2AllVersions(s.client)
	require.NoError(t, err)

	k3sVersions, err := kubernetesversions.ListK3SAllVersions(s.client)
	require.NoError(t, err)

	tests := []struct {
		name         string
		client       *rancher.Client
		clusterType  string
		nodeRoles    []provisioninginput.MachinePools
		minNodeCount int32
		maxNodeCount int32
		k8sVersions  []string
	}{
		{"RKE2_Auto_Scaler_KDM", s.standardUserClient, defaults.RKE2, nodeRolesStandard, 1, 3, rke2Versions},
		{"K3S_Auto_Scaler_KDM", s.standardUserClient, defaults.K3S, nodeRolesStandard, 1, 3, k3sVersions},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var clustersObj []*v1.SteveAPIObject
			for _, version := range tt.k8sVersions {
				clusterConfig := new(clusters.ClusterConfig)
				operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

				clusterConfig.MachinePools = tt.nodeRoles
				clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMinSize = &tt.minNodeCount
				clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMaxSize = &tt.maxNodeCount
				clusterConfig.KubernetesVersion = version

				provider := provisioning.CreateProvider(clusterConfig.Provider)
				machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)
				credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))

				logrus.Infof("Provisioning %s cluster with k8s version %s", tt.clusterType, version)
				cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
				require.NoError(t, err)

				clustersObj = append(clustersObj, cluster)
			}

			for _, cluster := range clustersObj {
				logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
				err = provisioning.VerifyClusterReady(s.client, cluster)
				require.NoError(t, err)

				logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
				err = deployment.VerifyClusterDeployments(s.client, cluster)
				require.NoError(t, err)

				logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
				err = pods.VerifyClusterPods(s.client, cluster)
				require.NoError(t, err)

				logrus.Infof("Verifying cluster autoscaler (%s)", cluster.Name)
				scaling.VerifyAutoscaler(t, s.client, cluster)
			}
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

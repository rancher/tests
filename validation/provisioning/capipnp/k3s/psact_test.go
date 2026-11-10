//go:build validation || capi

package k3s

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestPSACT(t *testing.T) {
	t.Parallel()

	k := capipnp.Setup(t, "k3s")

	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		psact        provisioninginput.PSACT
		client       *rancher.Client
	}{
		{"CAPI_K3S_Rancher_Privileged|3_etcd|2_cp|3_worker", nodeRolesStandard, provisioninginput.RancherPrivileged, k.StandardUserClient},
		{"CAPI_K3S_Rancher_Restricted|3_etcd|2_cp|3_worker", nodeRolesStandard, provisioninginput.RancherRestricted, k.StandardUserClient},
		{"CAPI_K3S_Rancher_Baseline|3_etcd|2_cp|3_worker", nodeRolesStandard, provisioninginput.RancherBaseline, k.Client},
	}

	for _, tt := range tests {
		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if tt.psact == provisioninginput.RancherBaseline {
				err := clusters.CreateRancherBaselinePSACT(k.Client, string(tt.psact))
				require.NoError(t, err)
			}

			clusterName := namegen.AppendRandomString(k.CapiConfig.ClusterNamePrefix)

			clusterManifest, err := capi.CreateClusterManifestFromDefaults(k.CapiConfig, tt.machinePools, clusterName,
				func(cluster *unstructured.Unstructured) error {
					return unstructured.SetNestedField(cluster.Object, string(tt.psact), "spec", "defaultPodSecurityAdmissionConfigurationTemplateName")
				})
			require.NoError(t, err)

			t.Cleanup(func() {
				logrus.Infof("Cleaning up cluster (%s)", clusterName)
				cleanupErr := capi.DeleteCAPICluster(k.Client, clusterName, clusterManifest)
				require.NoError(t, cleanupErr)
			})

			logrus.Infof("Creating cluster: (%s)", clusterName)
			err = capi.CreateCAPICluster(tt.client, clusterName, clusterManifest)
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

			logrus.Infof("Verifying cluster PSACT (%s)", cluster.Name)
			cluster, err = clusters.GetClusterByName(k.Client, clusterName)
			require.NoError(t, err)
			provisioning.VerifyPSACT(t, tt.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, k.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

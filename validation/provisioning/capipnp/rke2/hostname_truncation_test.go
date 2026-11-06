//go:build validation || capi

package rke2

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
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestHostnameTruncation(t *testing.T) {
	t.Parallel()

	k := capipnp.Setup(t, "rke2")

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	tests := []struct {
		name                    string
		hostnameLengthLimit     int64
		machinePoolLengthLimits []int64
	}{
		{"CAPI_RKE2_Hostname_Truncation|31_Characters", 31, []int64{25, 31, 63}},
		{"CAPI_RKE2_Hostname_Truncation|63_Characters", 63, []int64{25, 31, 63}},
	}

	for _, tt := range tests {
		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterName := namegen.AppendRandomString(k.CapiConfig.ClusterNamePrefix)

			clusterManifest, err := capi.CreateClusterManifestFromDefaults(k.CapiConfig, nodeRolesDedicated, clusterName,
				func(cluster *unstructured.Unstructured) error {
					if err := unstructured.SetNestedField(cluster.Object, tt.hostnameLengthLimit, "spec", "rkeConfig", "machinePoolDefaults", "hostnameLengthLimit"); err != nil {
						return err
					}

					rkeConfig := cluster.Object["spec"].(map[string]any)["rkeConfig"].(map[string]any)
					machinePools := rkeConfig["machinePools"].([]map[string]any)
					for index, machinePool := range machinePools {
						machinePool["name"] = namegen.RandStringLower(63)
						machinePool["hostnameLengthLimit"] = tt.machinePoolLengthLimits[index]
					}

					return nil
				})
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

			logrus.Infof("Verifying hostname truncation (%s)", cluster.Name)
			provisioning.VerifyHostnameLength(t, k.Client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(k.Client, k.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

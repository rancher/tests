//go:build validation || capi

package k3s

import (
	"testing"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
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
	"k8s.io/apimachinery/pkg/runtime"
)

func TestDataDirectories(t *testing.T) {
	t.Parallel()

	k := capipnp.Setup(t, "k3s")

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	splitDataDirectories := rkev1.DataDirectories{
		SystemAgent:  "/systemAgent",
		Provisioning: "/provisioning",
		K8sDistro:    "/k8sDistro",
	}

	groupedDataDirectories := rkev1.DataDirectories{
		SystemAgent:  "/groupDir/systemAgent",
		Provisioning: "/groupDir/provisioning",
		K8sDistro:    "/groupDir/k8sDistro",
	}

	tests := []struct {
		name            string
		dataDirectories rkev1.DataDirectories
		machinePools    []provisioninginput.MachinePools
	}{
		{"CAPI_K3S_Split_Data_Directories", splitDataDirectories, nodeRolesDedicated},
		{"CAPI_K3S_Grouped_Data_Directories", groupedDataDirectories, nodeRolesDedicated},
	}

	for _, tt := range tests {
		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterName := namegen.AppendRandomString(k.CapiConfig.ClusterNamePrefix)

			clusterManifest, err := capi.CreateClusterManifestFromDefaults(k.CapiConfig, tt.machinePools, clusterName,
				func(cluster *unstructured.Unstructured) error {
					directories, err := runtime.DefaultUnstructuredConverter.ToUnstructured(&tt.dataDirectories)
					if err != nil {
						return err
					}

					return unstructured.SetNestedMap(cluster.Object, directories, "spec", "rkeConfig", "dataDirectories")
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

			logrus.Infof("Verifying cluster data directories (%s)", cluster.Name)
			provisioning.VerifyDataDirectories(t, k.Client, cluster, k.CattleConfig)
		})

		params := provisioning.GetProvisioningSchemaParams(k.Client, k.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

//go:build validation || capi

package rke2

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	capi "github.com/rancher/tests/actions/capipnp"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/deleting/capipnp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestDeleteInitCAPIPnPMachine(t *testing.T) {
	t.Parallel()

	d := capipnp.Setup(t, defaults.RKE2, false)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"CAPI_PnP_RKE2_Delete_Init_Machine", d.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Cleaning up cluster (%s)", d.Cluster.Name)
			cleanupErr := capi.DeleteCAPICluster(d.Client, d.Cluster.Name, d.ClusterManifest)
			require.NoError(t, cleanupErr)
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			logrus.Infof("Deleting init machine on cluster (%s)", tt.cluster.Name)
			err := clusters.DeleteInitMachine(d.Client, tt.cluster.ID)
			require.NoError(t, err)

			err = provisioning.WaitClusterToBeUpgradedWithRetry(d.Client, tt.cluster.ID)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
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

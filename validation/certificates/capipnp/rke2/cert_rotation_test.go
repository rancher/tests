//go:build validation || capi

package rke2

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	capi "github.com/rancher/tests/actions/capipnp"
	"github.com/rancher/tests/actions/certificates"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/certificates/capipnp"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestCAPIPnPCertRotation(t *testing.T) {
	t.Parallel()

	c := capipnp.Setup(t, defaults.RKE2)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"CAPI_PnP_RKE2_Certificate_Rotation", c.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Cleaning up cluster (%s)", c.Cluster.Name)
			cleanupErr := capi.DeleteCAPICluster(c.Client, c.Cluster.Name, c.ClusterManifest)
			require.NoError(t, cleanupErr)
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			logrus.Infof("Getting current certificate rotation generation (%s)", tt.name)
			oldGeneration, err := certificates.GetCertRotationGeneration(c.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Rotating certificates on cluster (%s)", tt.cluster.Name)
			require.NoError(t, certificates.RotateCerts(c.Client, tt.cluster.Name))

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(c.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(c.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(c.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Getting new certificate rotation generation (%s)", tt.name)
			newGeneration, err := certificates.GetCertRotationGeneration(c.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying certificate rotation generation incremented (%s)", tt.cluster.Name)
			require.Greater(t, newGeneration, oldGeneration)
		})

		params := provisioning.GetProvisioningSchemaParams(c.Client, c.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

//go:build validation || capi

package rke2

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/validation/deleting/capipnp"
	"github.com/sirupsen/logrus"
)

func TestDeletingCAPIPnPCluster(t *testing.T) {
	t.Parallel()

	d := capipnp.Setup(t, defaults.RKE2, true)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"CAPI_PnP_RKE2_Delete_Cluster", d.Cluster},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logrus.Infof("Deleting cluster (%s)", tt.cluster.ID)
			extClusters.DeleteK3SRKE2Cluster(d.Client, tt.cluster.ID)

			logrus.Infof("Verifying cluster (%s) deletion", tt.cluster.ID)
			provisioning.VerifyDeleteRKE2K3SCluster(t, d.Client, tt.cluster.ID)
		})

		params := provisioning.GetProvisioningSchemaParams(d.Client, d.CattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

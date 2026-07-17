//go:build (validation || recurring || proxy || ipv6 || dualstack || infra.rke2k3s || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !sanity && !extended

package k3s

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/certificates"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	certTest "github.com/rancher/tests/validation/certificates"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestCertRotation(t *testing.T) {
	t.Parallel()

	c := certTest.Setup(t, defaults.K3S)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"K3S_Certificate_Rotation", c.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			c.Session.Cleanup()
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			oldCertificates, err := certificates.GetClusterCertificates(c.Client, tt.cluster.Name)
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

			newCertificates, err := certificates.GetClusterCertificates(c.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying certificates were rotated (%s)", tt.cluster.Name)
			isRotated := certificates.VerifyCertificateRotation(oldCertificates, newCertificates)
			require.True(t, isRotated)
		})

		params := provisioning.GetProvisioningSchemaParams(c.Client, c.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

//go:build validation || imported

package k3s

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	importedCert "github.com/rancher/tests/actions/certificates/imported"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	cert "github.com/rancher/tests/validation/certificates"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestCertRotationImported(t *testing.T) {
	t.Parallel()

	c := cert.ImportedSetup(t, defaults.K3S)

	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"K3S_Certificate_Rotation_Imported", c.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			c.Session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var err error

			logrus.Infof("Rotating certificates on cluster (%s)", tt.cluster.Name)
			err = importedCert.RotateCertificates(c.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying the imported cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReadyV3(c.Client, tt.cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(c.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(c.Client, tt.cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(c.Client, c.CattleConfig)
		if err := qase.UpdateSchemaParameters(tt.name, params); err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

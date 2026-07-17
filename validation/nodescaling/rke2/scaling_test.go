//go:build (validation || recurring || proxy || ipv6 || dualstack || infra.rke2k3s || cluster.custom || stress || pit.weekly || pit.elemental) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !cluster.any && !cluster.nodedriver && !sanity && !extended

package rke2

import (
	"testing"

	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/nodescaling"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestScalingNodePools(t *testing.T) {
	t.Parallel()

	s := nodescaling.Setup(t, defaults.RKE2)

	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd:     true,
		Quantity: 1,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
		Quantity:     1,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker:   true,
		Quantity: 1,
	}

	tests := []struct {
		name          string
		nodeRoles     machinepools.NodeRoles
		scaleQuantity int32
		cluster       *v1.SteveAPIObject
	}{
		{"RKE2_Scale_Control_Plane", nodeRolesControlPlane, 1, s.Cluster},
		{"RKE2_Scale_ETCD", nodeRolesEtcd, 1, s.Cluster},
		{"RKE2_Scale_Worker", nodeRolesWorker, 1, s.Cluster},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.Session.Cleanup()
		})

		var err error

		t.Run(tt.name, func(t *testing.T) {
			tt.nodeRoles.Quantity = tt.scaleQuantity
			logrus.Infof("Scaling up the node pool (%s)", tt.cluster.Name)
			tt.cluster, err = machinepools.ScaleMachinePool(s.Client, tt.cluster, tt.nodeRoles)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", tt.cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(s.Client, tt.cluster.Name)
			require.NoError(t, err)

			tt.nodeRoles.Quantity = -tt.scaleQuantity
			logrus.Infof("Scaling down the node pool (%s)", tt.cluster.Name)
			_, err = machinepools.ScaleMachinePool(s.Client, tt.cluster, tt.nodeRoles)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(s.Client, tt.cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", tt.cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(s.Client, tt.cluster.Name)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(s.Client, s.CattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

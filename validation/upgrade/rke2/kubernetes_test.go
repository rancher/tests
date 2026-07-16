//go:build validation || recurring || proxy || ipv6 || dualstack

package rke2

import (
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/upgrade"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	upgradeTest "github.com/rancher/tests/validation/upgrade"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestUpgradeKubernetes(t *testing.T) {
	t.Parallel()

	u := upgradeTest.Setup(t, defaults.RKE2, false)

	tests := []struct {
		name          string
		cluster       *v1.SteveAPIObject
		clusterConfig *clusters.ClusterConfig
	}{
		{"Upgrading_RKE2_cluster", u.Cluster, u.ClusterConfig},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			u.Session.Cleanup()
		})

		latestVersion, err := kubernetesversions.Default(u.Client, defaults.RKE2, nil)
		require.NoError(t, err)

		t.Run(tt.name, func(t *testing.T) {
			logrus.Infof("Upgrading cluster (%s) to the latest Kubernetes version", tt.cluster.Name)
			cluster, err := upgrade.UpgradeCluster(t, u.Client, tt.cluster, latestVersion[0])
			require.NoError(t, err)

			updatedClusterSpec := &provv1.ClusterSpec{}
			err = v1.ConvertToK8sType(cluster.Spec, updatedClusterSpec)
			require.NoError(t, err)
			require.Equal(t, latestVersion[0], updatedClusterSpec.KubernetesVersion)

			logrus.Infof("Cluster has been upgraded to: %s", updatedClusterSpec.KubernetesVersion)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(u.Client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(u.Client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(u.Client, cluster)
			require.NoError(t, err)
		})

		upgradedK8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "UpgradedK8sVersion", Values: []string{latestVersion[0]}}}
		params := provisioning.GetProvisioningSchemaParams(u.Client, u.CattleConfig)
		params = append(params, upgradedK8sParam)

		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

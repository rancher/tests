//go:build validation || eks

package hosted

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters/eks"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/clusters/hosted"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestProvisioningEKS(t *testing.T) {
	h := Setup(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"EKS_Hosted_Cluster", h.StandardUserClient},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			h.Session.Cleanup()
		})

		var eksClusterConfig eks.ClusterConfig
		operations.LoadObjectFromMap(eks.EKSClusterConfigConfigurationFileKey, h.CattleConfig, &eksClusterConfig)

		cloudCredentialID, err := hosted.CreateHostedClusterCredential(tt.client, "eks")
		require.NoError(t, err)

		if eksClusterConfig.KubernetesVersion == nil || *eksClusterConfig.KubernetesVersion == "" {
			eksClusterConfig.KubernetesVersion, err = hosted.DefaultHostedKubernetesVersion(tt.client, "eks", cloudCredentialID, "", "", eksClusterConfig.Region)
			require.NoError(t, err)
		}

		clusterName := namegenerator.AppendRandomString("ekshostcluster")
		clusterObject, err := eks.CreateEKSHostedCluster(tt.client, clusterName, cloudCredentialID, eksClusterConfig, false, false, false, false, nil)
		require.NoError(t, err)

		cluster, err := hosted.WaitForClusterToAppear(h.Client, "fleet-default/"+clusterObject.ID)
		require.NoError(t, err)

		logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
		err = provisioning.VerifyClusterReadyV3(h.Client, cluster.Name)
		require.NoError(t, err)

		logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
		err = deployment.VerifyClusterDeployments(h.Client, cluster)
		require.NoError(t, err)

		logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
		err = pods.VerifyClusterPods(h.Client, cluster)
		require.NoError(t, err)
	}
}

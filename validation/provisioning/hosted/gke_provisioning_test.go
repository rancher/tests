//go:build validation || gke

package hosted

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/clusters/gke"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/tests/actions/clusters/hosted"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

func TestProvisioningGKE(t *testing.T) {
	h := Setup(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"GKE_Hosted_Cluster", h.StandardUserClient},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			h.Session.Cleanup()
		})

		var gkeClusterConfig gke.ClusterConfig
		operations.LoadObjectFromMap(gke.GKEClusterConfigConfigurationFileKey, h.CattleConfig, &gkeClusterConfig)

		cloudCredentialID, err := hosted.CreateHostedClusterCredential(tt.client, "gke")
		require.NoError(t, err)

		if gkeClusterConfig.KubernetesVersion == nil || *gkeClusterConfig.KubernetesVersion == "" {
			gkeClusterConfig.KubernetesVersion, err = hosted.DefaultHostedKubernetesVersion(tt.client, "gke", cloudCredentialID, gkeClusterConfig.ProjectID, gkeClusterConfig.Zone, gkeClusterConfig.Region)
			require.NoError(t, err)
		}

		clusterName := namegenerator.AppendRandomString("gkehostcluster")
		clusterObject, err := gke.CreateGKEHostedCluster(tt.client, clusterName, cloudCredentialID, gkeClusterConfig, false, false, false, false, nil)
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

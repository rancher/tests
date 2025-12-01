//go:build (validation || infra.rke2k3s || recurring || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tests/validation/certificates"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CertRotationTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	rke2Cluster  *v1.SteveAPIObject
	k3sCluster   *v1.SteveAPIObject
}

func (c *CertRotationTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CertRotationTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(c.client)
	require.NoError(c.T(), err)

	c.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	c.cattleConfig, err = defaults.LoadPackageDefaults(c.cattleConfig, "")
	require.NoError(c.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(c.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, c.cattleConfig, clusterConfig)

	provider := provisioning.CreateProvider(clusterConfig.Provider)
	machineConfigSpec := provider.LoadMachineConfigFunc(c.cattleConfig)

	logrus.Info("Provisioning RKE2 cluster")
	c.rke2Cluster, err = resources.ProvisionRKE2K3SCluster(c.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(c.T(), err)

	logrus.Info("Provisioning K3S cluster")
	c.k3sCluster, err = resources.ProvisionRKE2K3SCluster(c.T(), standardUserClient, extClusters.K3SClusterType.String(), provider, *clusterConfig, machineConfigSpec, nil, true, false)
	require.NoError(c.T(), err)

}

func (c *CertRotationTestSuite) TestCertRotation() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2_Certificate_Rotation", c.rke2Cluster.ID},
		{"K3S_Certificate_Rotation", c.k3sCluster.ID},
	}

	for _, tt := range tests {
		cluster, err := c.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(c.T(), err)

		c.Run(tt.name, func() {
			logrus.Infof("Rotating certificates on cluster (%s)", cluster.Name)
			require.NoError(c.T(), certificates.RotateCerts(c.client, cluster.Name))

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(c.T(), c.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(c.T(), c.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(c.client, c.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestCertRotationTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationTestSuite))
}

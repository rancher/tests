//go:build validation || recurring

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/certificates"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CertRotationDualstackTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
}

func (c *CertRotationDualstackTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CertRotationDualstackTestSuite) SetupSuite() {
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

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, c.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(c.cattleConfig)

		logrus.Info("Provisioning RKE2 cluster")
		c.cluster, err = resources.ProvisionRKE2K3SCluster(c.T(), standardUserClient, defaults.RKE2, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(c.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		c.cluster, err = c.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + c.client.RancherConfig.ClusterName)
		require.NoError(c.T(), err)
	}
}

func (c *CertRotationDualstackTestSuite) TestCertRotationDualstack() {
	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"RKE2_Dualstack_Certificate_Rotation", c.cluster},
	}

	for _, tt := range tests {
		var err error
		c.Run(tt.name, func() {
			oldCertificates, err := certificates.GetClusterCertificates(c.client, tt.cluster.Name)
			require.NoError(c.T(), err)

			logrus.Infof("Rotating certificates on cluster (%s)", tt.cluster.Name)
			require.NoError(c.T(), certificates.RotateCerts(c.client, tt.cluster.Name))

			logrus.Infof("Verifying the cluster is ready (%s)", tt.cluster.Name)
			provisioning.VerifyClusterReady(c.T(), c.client, tt.cluster)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(c.client, tt.cluster)
			require.NoError(c.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(c.client, tt.cluster)
			require.NoError(c.T(), err)

			newCertificates, err := certificates.GetClusterCertificates(c.client, tt.cluster.Name)
			require.NoError(c.T(), err)

			logrus.Infof("Verifying certificates were rotated (%s)", tt.cluster.Name)
			isRotated := certificates.VerifyCertificateRotation(oldCertificates, newCertificates)
			require.True(c.T(), isRotated)
		})

		params := provisioning.GetProvisioningSchemaParams(c.client, c.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestCertRotationDualstackTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationDualstackTestSuite))
}

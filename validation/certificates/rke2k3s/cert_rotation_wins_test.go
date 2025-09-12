//go:build (validation || infra.rke2k3s || recurring || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package rke2k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/providers"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/validation/certificates"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CertRotationWindowsTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	rke2ClusterID string
}

func (c *CertRotationWindowsTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CertRotationWindowsTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)

	c.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(c.client)
	require.NoError(c.T(), err)

	c.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(c.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, c.cattleConfig, clusterConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, c.cattleConfig, awsEC2Configs)

	if clusterConfig.Provider != providers.Vsphere {
		c.T().Skip("Test requires vSphere provider")
	}

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
		provisioninginput.WindowsMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 1
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 1
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 1
	nodeRolesStandard[3].MachinePoolConfig.Quantity = 1

	clusterConfig.MachinePools = nodeRolesStandard

	logrus.Info("Provisioning RKE2 windows cluster")
	c.rke2ClusterID, err = resources.ProvisionRKE2K3SCluster(c.T(), standardUserClient, extClusters.RKE2ClusterType.String(), clusterConfig, awsEC2Configs, true, false)
	require.NoError(c.T(), err)
}

func (c *CertRotationWindowsTestSuite) TestCertRotationWindows() {
	tests := []struct {
		name      string
		clusterID string
	}{
		{"RKE2_Windows_Certificate_Rotation", c.rke2ClusterID},
	}

	for _, tt := range tests {
		cluster, err := c.client.Steve.SteveType(stevetypes.Provisioning).ByID(tt.clusterID)
		require.NoError(c.T(), err)

		c.Run(tt.name, func() {
			logrus.Infof("Rotating certificates on cluster (%s)", cluster.Name)
			require.NoError(c.T(), certificates.RotateCerts(c.client, cluster.Name))

			logrus.Infof("Verifying cluster (%s)", cluster.Name)
			provisioning.VerifyCluster(c.T(), c.client, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(c.client, c.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestCertRotationWindowsTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationWindowsTestSuite))
}

//go:build (validation || infra.rke2k3s || recurring || cluster.any || stress) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !sanity && !extended

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/providers"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/certificates"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CertRotationWindowsTestSuite struct {
	suite.Suite
	session      *session.Session
	client       *rancher.Client
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
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

	c.cattleConfig, err = defaults.LoadPackageDefaults(c.cattleConfig, "")
	require.NoError(c.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(c.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, c.cattleConfig, clusterConfig)

	awsEC2Configs := new(ec2.AWSEC2Configs)
	operations.LoadObjectFromMap(ec2.ConfigurationFileKey, c.cattleConfig, awsEC2Configs)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, c.cattleConfig, rancherConfig)

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

	if rancherConfig.ClusterName == "" {
		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(c.cattleConfig)

		logrus.Info("Provisioning RKE2 windows cluster")
		c.cluster, err = resources.ProvisionRKE2K3SCluster(c.T(), standardUserClient, extClusters.RKE2ClusterType.String(), provider, *clusterConfig, machineConfigSpec, awsEC2Configs, true, false)
		require.NoError(c.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		c.cluster, err = c.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + c.client.RancherConfig.ClusterName)
		require.NoError(c.T(), err)
	}
}

func (c *CertRotationWindowsTestSuite) TestCertRotationWindows() {
	tests := []struct {
		name    string
		cluster *v1.SteveAPIObject
	}{
		{"RKE2_Windows_Certificate_Rotation", c.cluster},
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

func TestCertRotationWindowsTestSuite(t *testing.T) {
	suite.Run(t, new(CertRotationWindowsTestSuite))
}

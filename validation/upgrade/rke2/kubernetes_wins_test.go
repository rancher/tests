//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
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
	"github.com/rancher/tests/actions/upgrade"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type UpgradeWindowsKubernetesTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	cluster       *v1.SteveAPIObject
}

func (u *UpgradeWindowsKubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeWindowsKubernetesTestSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	client, err := rancher.NewClient("", u.session)
	require.NoError(u.T(), err)

	u.client = client

	u.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	u.cattleConfig, err = defaults.LoadPackageDefaults(u.cattleConfig, "")
	require.NoError(u.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(u.T(), err)

	u.clusterConfig = new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.cattleConfig, u.clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, u.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		standardUserClient, _, _, err := standard.CreateStandardUser(u.client)
		require.NoError(u.T(), err)

		awsEC2Configs := new(ec2.AWSEC2Configs)
		operations.LoadObjectFromMap(ec2.ConfigurationFileKey, u.cattleConfig, awsEC2Configs)

		nodeRolesStandard := []provisioninginput.MachinePools{
			provisioninginput.EtcdMachinePool,
			provisioninginput.ControlPlaneMachinePool,
			provisioninginput.WorkerMachinePool,
			provisioninginput.WindowsMachinePool,
		}

		nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
		nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
		nodeRolesStandard[2].MachinePoolConfig.Quantity = 3
		nodeRolesStandard[3].MachinePoolConfig.Quantity = 1

		u.clusterConfig.MachinePools = nodeRolesStandard

		provider := provisioning.CreateProvider(u.clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(u.cattleConfig)

		logrus.Info("Provisioning RKE2 Windows cluster")
		u.cluster, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, defaults.RKE2, provider, *u.clusterConfig, machineConfigSpec, awsEC2Configs, false, true)
		require.NoError(u.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		u.cluster, err = u.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + rancherConfig.ClusterName)
		require.NoError(u.T(), err)
	}
}

func (u *UpgradeWindowsKubernetesTestSuite) TestUpgradeWindowsKubernetes() {
	tests := []struct {
		name          string
		cluster       *v1.SteveAPIObject
		clusterConfig *clusters.ClusterConfig
	}{
		{"Upgrading_RKE2_Windows_cluster", u.cluster, u.clusterConfig},
	}

	for _, tt := range tests {
		latestVersion, err := kubernetesversions.Default(u.client, defaults.RKE2, nil)
		require.NoError(u.T(), err)

		u.Run(tt.name, func() {
			logrus.Infof("Upgrading cluster (%s) to the latest Kubernetes version", tt.cluster.Name)
			cluster, err := upgrade.UpgradeCluster(u.T(), u.client, u.cluster, latestVersion[0])
			require.NoError(u.T(), err)

			updatedClusterSpec := &provv1.ClusterSpec{}
			err = v1.ConvertToK8sType(cluster.Spec, updatedClusterSpec)
			require.NoError(u.T(), err)
			require.Equal(u.T(), latestVersion[0], updatedClusterSpec.KubernetesVersion)

			logrus.Infof("Cluster has been upgraded to: %s", updatedClusterSpec.KubernetesVersion)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(u.client, cluster)
			require.NoError(u.T(), err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(u.client, cluster)
			require.NoError(u.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(u.client, cluster)
			require.NoError(u.T(), err)
		})

		upgradedK8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "UpgradedK8sVersion", Values: []string{tt.clusterConfig.KubernetesVersion}}}
		params := provisioning.GetProvisioningSchemaParams(u.client, u.cattleConfig)
		params = append(params, upgradedK8sParam)

		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestWindowsKubernetesUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeWindowsKubernetesTestSuite))
}

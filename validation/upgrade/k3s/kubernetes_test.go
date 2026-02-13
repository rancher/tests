//go:build validation || recurring

package k3s

import (
	"os"
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
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

type UpgradeKubernetesTestSuite struct {
	suite.Suite
	session       *session.Session
	client        *rancher.Client
	cattleConfig  map[string]any
	clusterConfig *clusters.ClusterConfig
	cluster       *v1.SteveAPIObject
}

func (u *UpgradeKubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeKubernetesTestSuite) SetupSuite() {
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

		provider := provisioning.CreateProvider(u.clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(u.cattleConfig)

		logrus.Info("Provisioning K3S cluster")
		u.cluster, err = resources.ProvisionRKE2K3SCluster(u.T(), standardUserClient, defaults.K3S, provider, *u.clusterConfig, machineConfigSpec, nil, false, false)
		require.NoError(u.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		u.cluster, err = u.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + rancherConfig.ClusterName)
		require.NoError(u.T(), err)
	}
}

func (u *UpgradeKubernetesTestSuite) TestUpgradeKubernetes() {
	tests := []struct {
		name          string
		cluster       *v1.SteveAPIObject
		clusterConfig *clusters.ClusterConfig
	}{
		{"Upgrading_K3S_cluster", u.cluster, u.clusterConfig},
	}

	for _, tt := range tests {
		latestVersion, err := kubernetesversions.Default(u.client, defaults.K3S, nil)
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

func TestKubernetesUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeKubernetesTestSuite))
}

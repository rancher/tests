//go:build validation

package rke1

import (
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/rancher/shepherd/clients/rancher"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/rancher/tests/validation/upgrade"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type UpgradeRKE1KubernetesTestSuite struct {
	suite.Suite
	session            *session.Session
	client             *rancher.Client
	provisioningConfig *provisioninginput.Config
	rke1ClusterID      string
}

func (u *UpgradeRKE1KubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeRKE1KubernetesTestSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	u.provisioningConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, u.provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(u.T(), err)

	u.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(u.client)
	require.NoError(u.T(), err)

	nodeRolesStandard := []provisioninginput.NodePools{
		provisioninginput.EtcdNodePool,
		provisioninginput.ControlPlaneNodePool,
		provisioninginput.WorkerNodePool,
	}

	nodeRolesStandard[0].NodeRoles.Quantity = 3
	nodeRolesStandard[1].NodeRoles.Quantity = 2
	nodeRolesStandard[2].NodeRoles.Quantity = 3

	u.provisioningConfig.NodePools = nodeRolesStandard

	u.rke1ClusterID, err = resources.ProvisionRKE1Cluster(u.T(), standardUserClient, u.provisioningConfig, false, false)
	require.NoError(u.T(), err)
}

func (u *UpgradeRKE1KubernetesTestSuite) TestUpgradeRKE1Kubernetes() {
	tests := []struct {
		name        string
		client      *rancher.Client
		clusterType string
	}{
		{"Upgrading_RKE1_cluster", u.client, extClusters.RKE1ClusterType.String()},
	}

	var params []upstream.TestCaseParameterCreate
	for _, tt := range tests {
		version, err := kubernetesversions.Default(u.client, tt.clusterType, nil)
		require.NoError(u.T(), err)

		clusterResp, err := u.client.Management.Cluster.ByID(u.rke1ClusterID)
		require.NoError(u.T(), err)

		testConfig := clusters.ConvertConfigToClusterConfig(u.provisioningConfig)
		testConfig.KubernetesVersion = version[0]

		u.Run(tt.name, func() {
			upgrade.DownstreamCluster(&u.Suite, tt.name, u.client, clusterResp.Name, testConfig, u.rke1ClusterID, testConfig.KubernetesVersion, true)
		})

		k8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "K8sVersion", Values: []string{clusterResp.RancherKubernetesEngineConfig.Version}}}
		upgradedK8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "UpgradedK8sVersion", Values: []string{testConfig.KubernetesVersion}}}

		params = append(params, k8sParam, upgradedK8sParam)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestUpgradeRKE1KubernetesTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeRKE1KubernetesTestSuite))
}

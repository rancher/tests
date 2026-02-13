//go:build validation

package upgrade

import (
	"testing"

	upstream "github.com/qase-tms/qase-go/qase-api-client"
	"github.com/rancher/shepherd/clients/rancher"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/upgrade"
	"github.com/rancher/tests/actions/upgradeinput"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type UpgradeKubernetesTestSuite struct {
	suite.Suite
	session  *session.Session
	client   *rancher.Client
	clusters []upgradeinput.Cluster
}

func (u *UpgradeKubernetesTestSuite) TearDownSuite() {
	u.session.Cleanup()
}

func (u *UpgradeKubernetesTestSuite) SetupSuite() {
	testSession := session.NewSession()
	u.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(u.T(), err)

	u.client = client

	clusters, err := upgradeinput.LoadUpgradeKubernetesConfig(client)
	require.NoError(u.T(), err)

	u.clusters = clusters
}

func (u *UpgradeKubernetesTestSuite) TestUpgradeKubernetes() {
	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"Upgrading_Local_Cluster", u.client},
	}

	var testConfig *clusters.ClusterConfig
	var params []upstream.TestCaseParameterCreate

	for _, tt := range tests {
		if u.clusters[0].VersionToUpgrade == "" {
			u.T().Skip(u.T(), u.clusters[0].VersionToUpgrade, "Kubernetes version to upgrade is not provided, skipping the test")
		}

		testConfig = clusters.ConvertConfigToClusterConfig(&u.clusters[0].ProvisioningInput)
		testConfig.KubernetesVersion = u.clusters[0].VersionToUpgrade

		u.Run(tt.name, func() {
			upgrade.UpgradeLocalCluster(&u.Suite, u.client, testConfig, u.clusters[0])
		})

		clusterMeta, err := extensionscluster.NewClusterMeta(tt.client, u.clusters[0].Name)
		require.NoError(u.T(), err)

		upgradedCluster, err := tt.client.Management.Cluster.ByID(clusterMeta.ID)
		require.NoError(u.T(), err)

		k8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "K8sVersion", Values: []string{testConfig.KubernetesVersion}}}
		upgradedK8sParam := upstream.TestCaseParameterCreate{ParameterSingle: &upstream.ParameterSingle{Title: "UpgradedK8sVersion", Values: []string{upgradedCluster.Version.GitVersion}}}

		params = append(params, k8sParam, upgradedK8sParam)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestKubernetesUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(UpgradeKubernetesTestSuite))
}

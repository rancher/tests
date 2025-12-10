//go:build validation || harvester

package harvester

import (
	"strings"
	"testing"

	"github.com/rancher/shepherd/clients/harvester"
	"github.com/rancher/shepherd/clients/rancher"
	extensioncharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/uiplugins"
	interoperablecharts "github.com/rancher/tests/interoperability/charts"
	harvesteraction "github.com/rancher/tests/interoperability/harvester"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	localCluster                   = "local"
	harvesterUIExtensionGitRepoURL = "https://github.com/harvester/harvester-ui-extension"
	harvesterUIExtensionGitBranch  = "gh-pages"
	harvesterExtensionName         = "harvester"
)

type HarvesterTestSuite struct {
	suite.Suite
	client          *rancher.Client
	session         *session.Session
	harvesterClient *harvester.Client
}

func (h *HarvesterTestSuite) TearDownSuite() {
	h.session.Cleanup()
}

func (h *HarvesterTestSuite) SetupSuite() {
	h.session = session.NewSession()

	client, err := rancher.NewClient("", h.session)
	require.NoError(h.T(), err)

	h.client = client

	h.harvesterClient, err = harvester.NewClient("", h.session)
	require.NoError(h.T(), err)

	userConfig := new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, userConfig)

	h.session.RegisterCleanupFunc(func() error {
		return harvesteraction.ResetHarvesterRegistration(h.harvesterClient)
	})

	err = extensioncharts.CreateChartRepoFromGithub(client.Steve, harvesterUIExtensionGitRepoURL, harvesterUIExtensionGitBranch, harvesterExtensionName)
	if err != nil {
		if !strings.Contains(err.Error(), "already exists") {
			require.NoError(h.T(), err)
		}
	}

	uiExtensionObject, err := extensioncharts.GetChartStatus(client, localCluster, interoperablecharts.ExtensionNamespace, interoperablecharts.HarvesterExtensionName)
	require.NoError(h.T(), err)

	if !uiExtensionObject.IsAlreadyInstalled {
		latestUIPluginVersion, err := h.client.Catalog.GetLatestChartVersion(interoperablecharts.HarvesterExtensionName, interoperablecharts.HarvesterExtensionName)
		require.NoError(h.T(), err)

		extensionOptions := &uiplugins.ExtensionOptions{
			ChartName:   interoperablecharts.HarvesterExtensionName,
			ReleaseName: interoperablecharts.HarvesterExtensionName,
			Version:     latestUIPluginVersion,
		}

		err = uiplugins.InstallUIPlugin(client, extensionOptions, interoperablecharts.HarvesterExtensionName)
		require.NoError(h.T(), err)
	}

}

func (h *HarvesterTestSuite) TestImport() {
	harvesterInRancherID, err := harvesteraction.RegisterHarvesterWithRancher(h.client, h.harvesterClient)
	require.NoError(h.T(), err)
	logrus.Info(harvesterInRancherID)

	cluster, err := h.client.Management.Cluster.ByID(harvesterInRancherID)
	require.NoError(h.T(), err)

	kubeConfig, err := h.client.Management.Cluster.ActionGenerateKubeconfig(cluster)
	require.NoError(h.T(), err)

	var harvesterCredentialConfig cloudcredentials.HarvesterCredentialConfig

	harvesterCredentialConfig.ClusterID = harvesterInRancherID
	harvesterCredentialConfig.ClusterType = "imported"
	harvesterCredentialConfig.KubeconfigContent = kubeConfig.Config

	shepherdConfig.UpdateConfig(cloudcredentials.HarvesterCredentialConfigurationFileKey, harvesterCredentialConfig)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestHarvesterTestSuite(t *testing.T) {
	suite.Run(t, new(HarvesterTestSuite))
}

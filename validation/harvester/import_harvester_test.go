//go:build validation || harvester

package harvester

import (
	"context"
	"strings"
	"testing"
	"time"

	catalogv1 "github.com/rancher/rancher/pkg/apis/catalog.cattle.io/v1"
	"github.com/rancher/shepherd/clients/harvester"
	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensioncharts "github.com/rancher/shepherd/extensions/charts"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/defaults"
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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	kwait "k8s.io/apimachinery/pkg/util/wait"
)

const (
	localCluster                   = "local"
	harvesterUIExtensionGitRepoURL = "https://github.com/harvester/harvester-ui-extension"
	harvesterUIExtensionGitBranch  = "gh-pages"
	harvesterExtensionName         = "harvester"
	clusterRepoType                = "catalog.cattle.io.clusterrepo"
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
		var latestUIPluginVersion string
		var chartVersionErr error
		var lastRepoRefresh time.Time
		err = kwait.PollUntilContextTimeout(context.Background(), defaults.FiveSecondTimeout, defaults.FifteenMinuteTimeout, true, func(context.Context) (bool, error) {
			latestUIPluginVersion, chartVersionErr = h.client.Catalog.GetLatestChartVersion(interoperablecharts.HarvesterExtensionName, interoperablecharts.HarvesterExtensionName)
			if chartVersionErr == nil {
				return true, nil
			}

			if time.Since(lastRepoRefresh) >= defaults.OneMinuteTimeout {
				refreshed, refreshErr := refreshBackedOffClusterRepo(h.client, harvesterExtensionName)
				if refreshErr != nil {
					logrus.Warnf("unable to refresh cluster repo %s: %v", harvesterExtensionName, refreshErr)
				} else if refreshed {
					lastRepoRefresh = time.Now()
					logrus.Infof("cluster repo %s failed to download, forcing a new download", harvesterExtensionName)
				}
			}

			return false, nil
		})
		require.NoError(h.T(), err, "harvester UI extension chart version never became available: %v", chartVersionErr)

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

func refreshBackedOffClusterRepo(client *rancher.Client, repoName string) (bool, error) {
	repoObject, err := client.Steve.SteveType(clusterRepoType).ByID(repoName)
	if err != nil {
		return false, err
	}

	clusterRepo := &catalogv1.ClusterRepo{}
	err = steveV1.ConvertToK8sType(repoObject, clusterRepo)
	if err != nil {
		return false, err
	}

	if clusterRepo.Status.NextRetryAt.IsZero() {
		return false, nil
	}

	clusterRepo.Spec.ForceUpdate = &metav1.Time{Time: time.Now()}
	_, err = client.Steve.SteveType(clusterRepoType).Update(repoObject, clusterRepo)
	if err != nil {
		return false, err
	}

	return true, nil
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestHarvesterTestSuite(t *testing.T) {
	suite.Run(t, new(HarvesterTestSuite))
}

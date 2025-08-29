//go:build validation

package harvester

import (
	"testing"

	"github.com/rancher/shepherd/clients/harvester"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/sirupsen/logrus"

	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	harvesteraction "github.com/rancher/tests/interoperability/harvester"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HarvesterTestSuite struct {
	suite.Suite
	client          *rancher.Client
	session         *session.Session
	clusterID       string
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

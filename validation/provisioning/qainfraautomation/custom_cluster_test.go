//go:build validation || qainfraautomation

package qainfraautomation

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/interoperability/qainfraautomation"
	qaconfig "github.com/rancher/tests/interoperability/qainfraautomation/config"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CustomClusterTestSuite struct {
	suite.Suite
	client  *rancher.Client
	session *session.Session
	cfg     *qaconfig.Config
}

func (s *CustomClusterTestSuite) TearDownSuite() {
	s.session.Cleanup()
}

func (s *CustomClusterTestSuite) SetupSuite() {
	s.session = session.NewSession()

	client, err := rancher.NewClient("", s.session)
	require.NoError(s.T(), err)
	s.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.cfg = new(qaconfig.Config)
	operations.LoadObjectFromMap(qaconfig.ConfigurationFileKey, cattleConfig, s.cfg)

	require.NotNil(s.T(), s.cfg.Harvester, "harvester config is required under qaInfraAutomation.harvester")
	require.NotNil(s.T(), s.cfg.CustomCluster, "customCluster config is required under qaInfraAutomation.customCluster")
}

// TestHarvesterCustomCluster provisions Harvester VMs via OpenTofu, creates a Rancher
// custom downstream cluster via Ansible, verifies it is ready, then destroys it.
func (s *CustomClusterTestSuite) TestHarvesterCustomCluster() {
	clusterObj, cleanup, err := qainfraautomation.ProvisionHarvesterCustomCluster(
		s.client,
		s.cfg,
		s.cfg.CustomCluster,
	)
	require.NoError(s.T(), err)
	s.T().Cleanup(func() {
		require.NoError(s.T(), cleanup())
	})

	require.NotNil(s.T(), clusterObj, "expected a non-nil cluster object")
	s.T().Logf("cluster %q is ready", clusterObj.Name)
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestCustomClusterTestSuite(t *testing.T) {
	suite.Run(t, new(CustomClusterTestSuite))
}

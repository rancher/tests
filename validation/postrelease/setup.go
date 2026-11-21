package postrelease

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/prime"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type postRelease struct {
	suite.Suite
	Client        *rancher.Client
	Session       *session.Session
	CattleConfig  map[string]any
	ClusterConfig *clusters.ClusterConfig
	RancherConfig *rancher.Config
	PrimeConfig   *prime.Config
}

func Setup(t *testing.T, clusterType string) *postRelease {
	p := &postRelease{}

	testSession := session.NewSession()
	p.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	p.Client = client

	p.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, p.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, p.CattleConfig, clusterConfig)

	p.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, p.CattleConfig, rancherConfig)

	p.RancherConfig = rancherConfig

	primeConfig := new(prime.Config)
	operations.LoadObjectFromMap(prime.ConfigurationFileKey, p.CattleConfig, primeConfig)

	p.PrimeConfig = primeConfig

	return p
}

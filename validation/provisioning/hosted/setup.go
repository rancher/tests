package hosted

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type hostedTest struct {
	suite.Suite
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	Session            *session.Session
	CattleConfig       map[string]any
	ClusterConfig      *clusters.ClusterConfig
	rancherConfig      *rancher.Config
	Cluster            *management.Cluster
}

func Setup(t *testing.T) *hostedTest {
	h := &hostedTest{}

	testSession := session.NewSession()
	h.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	h.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(h.Client)
	require.NoError(t, err)

	h.StandardUserClient = standardUserClient

	h.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	h.CattleConfig, err = defaults.LoadPackageDefaults(h.CattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, h.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, h.CattleConfig, clusterConfig)

	h.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, h.CattleConfig, rancherConfig)

	h.rancherConfig = rancherConfig

	return h
}

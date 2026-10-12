//go:build validation

package remotedialerproxy

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type rdpTest struct {
	client       *rancher.Client
	session      *session.Session
	cattleConfig map[string]any
}

func rdpSetup(t *testing.T) rdpTest {
	var r rdpTest

	s := session.NewSession()
	r.session = s

	client, err := rancher.NewClient("", s)
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "", "")
	require.NoError(t, err)

	logCfg := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, logCfg)
	require.NoError(t, logging.SetLogger(logCfg))

	return r
}

// getExistingCluster resolves the cluster named by rancher.clusterName in the config.
func getExistingCluster(t *testing.T, client *rancher.Client) *steveV1.SteveAPIObject {
	clusterName := client.RancherConfig.ClusterName
	require.NotEmpty(t, clusterName, "rancher.clusterName must be set in the config")

	clusterID, err := extClusters.GetV1ProvisioningClusterByName(client, clusterName)
	require.NoError(t, err)

	cluster, err := client.Steve.SteveType(stevetypes.Provisioning).ByID(clusterID)
	require.NoError(t, err)

	return cluster
}

func TestRemotedialerProxy(t *testing.T) {
	r := rdpSetup(t)

	t.Cleanup(func() {
		r.session.Cleanup()
	})

	cluster := getExistingCluster(t, r.client)
	logrus.Infof("Using existing downstream cluster: %s", cluster.Name)

	require.NoError(t, provisioning.VerifyClusterReady(r.client, cluster))

	t.Run("RemotedialerProxy_Validations", func(t *testing.T) {
		remotedialerProxyValidations(t, r.client, cluster)
	})

	params := provisioning.GetProvisioningSchemaParams(r.client, r.cattleConfig)
	_ = qase.UpdateSchemaParameters("RemotedialerProxy_Validations", params)
}

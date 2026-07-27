package capipnp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/capipnp"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/rbac"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type capipnpTest struct {
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	Session            *session.Session
	CattleConfig       map[string]any
	CapiConfig         *capipnp.Config
	RancherConfig      *rancher.Config
}

func Setup(t *testing.T, clusterType string) capipnpTest {
	var c capipnpTest

	c.CapiConfig = new(capipnp.Config)

	testSession := session.NewSession()
	c.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	c.Client = client

	localClusterID, err := extclusters.GetClusterIDByName(c.Client, extclusterapi.LocalCluster)
	require.NoError(t, err)

	localCluster, err := c.Client.Management.Cluster.ByID(localClusterID)
	require.NoError(t, err)

	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(c.Client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), localCluster, nil)
	require.NoError(t, err)

	c.StandardUserClient = standardUserClient

	c.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))
	operations.LoadObjectFromMap(capipnp.ConfigurationFileKey, c.CattleConfig, c.CapiConfig)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	c.CattleConfig, err = defaults.SetK8sDefault(c.Client, clusterType, c.CattleConfig)
	require.NoError(t, err)

	resolvedK8sVersion, err := operations.GetValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, c.CattleConfig)
	require.NoError(t, err)

	if resolvedK8sVersion != nil {
		c.CapiConfig.KubernetesVersion, _ = resolvedK8sVersion.(string)
	}

	_, setupFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	capipnpDir := filepath.Dir(setupFile)
	provisioningDir := filepath.Dir(capipnpDir)

	providerManifestsPath := filepath.Join(provisioningDir, "resources", "capipnp", "providers", c.CapiConfig.Provider)
	c.CapiConfig.ClusterYAML = filepath.Join(providerManifestsPath, clusterType+"-cluster.yaml")
	providerManifest := filepath.Join(providerManifestsPath, "provider.yaml")
	credentialsManifest := filepath.Join(providerManifestsPath, "creds.yaml")
	identityManifest := filepath.Join(providerManifestsPath, "identity.yaml")

	logrus.Debugf("Applying %s provider manifest...", c.CapiConfig.Provider)
	err = capipnp.CreateProviderManifestFromPath(c.Client, c.CapiConfig, providerManifest)
	require.NoError(t, err)

	logrus.Debugf("Applying credentials manifest...")
	err = capipnp.CreateProviderManifestFromPath(c.Client, c.CapiConfig, credentialsManifest)
	require.NoError(t, err)

	logrus.Debugf("Waiting for previous manifests to be active...")
	err = capipnp.WaitProviderPrerequisitesReady(c.Client, c.CapiConfig)
	require.NoError(t, err)

	logrus.Debugf("Applying cluster static identity manifest...")
	err = capipnp.CreateProviderManifestFromPath(c.Client, c.CapiConfig, identityManifest)
	require.NoError(t, err)

	return c
}

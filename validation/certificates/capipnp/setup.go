package capipnp

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	extclusterapi "github.com/rancher/shepherd/extensions/kubeapi/cluster"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/capipnp"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type certRotationTest struct {
	suite.Suite
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	Session            *session.Session
	CattleConfig       map[string]any
	ClusterConfig      *clusters.ClusterConfig
	RancherConfig      *rancher.Config
	CapiConfig         *capipnp.Config
	Cluster            *v1.SteveAPIObject
	ClusterManifest    []byte
}

func Setup(t *testing.T, clusterType string) *certRotationTest {
	c := &certRotationTest{}

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

	_, setupFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	capipnpDir := filepath.Dir(setupFile)
	certificatesDir := filepath.Dir(capipnpDir)
	validationDir := filepath.Dir(certificatesDir)
	provisioningDir := filepath.Join(validationDir, "provisioning")

	provisioningDefaults := config.LoadConfigFromFile(filepath.Join(provisioningDir, "defaults", "defaults.yaml"))
	deletingDefaults := config.LoadConfigFromFile(filepath.Join(certificatesDir, "defaults", "defaults.yaml"))
	validationDefaults, err := defaults.DeepMerge(deletingDefaults, provisioningDefaults, true)
	require.NoError(t, err)

	c.CattleConfig, err = defaults.DeepMerge(c.CattleConfig, validationDefaults, true)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, c.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, c.CattleConfig, clusterConfig)

	c.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, c.CattleConfig, rancherConfig)

	c.RancherConfig = rancherConfig

	capiConfig := new(capipnp.Config)
	operations.LoadObjectFromMap(capipnp.ConfigurationFileKey, c.CattleConfig, capiConfig)

	c.CapiConfig = capiConfig

	providerManifestsPath := filepath.Join(provisioningDir, "resources", "capipnp", "providers", c.CapiConfig.Provider)
	c.CapiConfig.ClusterYAML = filepath.Join(providerManifestsPath, clusterType+"-cluster.yaml")
	providerManifest := filepath.Join(providerManifestsPath, "provider.yaml")
	credentialsManifest := filepath.Join(providerManifestsPath, "creds.yaml")
	identityManifest := filepath.Join(providerManifestsPath, "identity.yaml")

	if c.RancherConfig.ClusterName == "" {
		c.CattleConfig, err = defaults.SetK8sDefault(c.Client, clusterType, c.CattleConfig)
		require.NoError(t, err)

		resolvedK8sVersion, err := operations.GetValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, c.CattleConfig)
		require.NoError(t, err)

		if resolvedK8sVersion != nil {
			c.CapiConfig.KubernetesVersion, _ = resolvedK8sVersion.(string)
		}

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

		clusterName := namegen.AppendRandomString(c.CapiConfig.ClusterNamePrefix)

		c.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(c.CapiConfig, c.ClusterConfig.MachinePools, clusterName)
		require.NoError(t, err)

		logrus.Infof("Provisioning %s cluster", clusterType)
		err = capipnp.CreateCAPICluster(c.StandardUserClient, clusterName, c.ClusterManifest)
		require.NoError(t, err)

		cluster, err := clusters.GetClusterByName(c.Client, clusterName)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(c.Client, cluster)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(c.Client, cluster)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(c.Client, cluster)
		require.NoError(t, err)

		c.Cluster, err = c.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + cluster.Name)
		require.NoError(t, err)
	} else {
		c.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(c.CapiConfig, c.ClusterConfig.MachinePools, c.RancherConfig.ClusterName)
		require.NoError(t, err)

		logrus.Infof("Using existing cluster %s", c.RancherConfig.ClusterName)
		c.Cluster, err = c.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + c.RancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return c
}

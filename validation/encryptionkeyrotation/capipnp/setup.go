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

type encryptionKeyRotationTest struct {
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

func Setup(t *testing.T, clusterType string) *encryptionKeyRotationTest {
	e := &encryptionKeyRotationTest{}

	testSession := session.NewSession()
	e.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	e.Client = client

	localClusterID, err := extclusters.GetClusterIDByName(e.Client, extclusterapi.LocalCluster)
	require.NoError(t, err)

	localCluster, err := e.Client.Management.Cluster.ByID(localClusterID)
	require.NoError(t, err)

	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(e.Client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), localCluster, nil)
	require.NoError(t, err)

	e.StandardUserClient = standardUserClient

	e.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	_, setupFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	capipnpDir := filepath.Dir(setupFile)
	ekrDir := filepath.Dir(capipnpDir)
	validationDir := filepath.Dir(ekrDir)
	provisioningDir := filepath.Join(validationDir, "provisioning")

	provisioningDefaults := config.LoadConfigFromFile(filepath.Join(provisioningDir, "defaults", "defaults.yaml"))
	deletingDefaults := config.LoadConfigFromFile(filepath.Join(ekrDir, "defaults", "defaults.yaml"))
	validationDefaults, err := defaults.DeepMerge(deletingDefaults, provisioningDefaults, true)
	require.NoError(t, err)

	e.CattleConfig, err = defaults.DeepMerge(e.CattleConfig, validationDefaults, true)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, e.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, e.CattleConfig, clusterConfig)

	e.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, e.CattleConfig, rancherConfig)

	e.RancherConfig = rancherConfig

	capiConfig := new(capipnp.Config)
	operations.LoadObjectFromMap(capipnp.ConfigurationFileKey, e.CattleConfig, capiConfig)

	e.CapiConfig = capiConfig

	providerManifestsPath := filepath.Join(provisioningDir, "resources", "capipnp", "providers", e.CapiConfig.Provider)
	e.CapiConfig.ClusterYAML = filepath.Join(providerManifestsPath, clusterType+"-cluster.yaml")
	providerManifest := filepath.Join(providerManifestsPath, "provider.yaml")
	credentialsManifest := filepath.Join(providerManifestsPath, "creds.yaml")
	identityManifest := filepath.Join(providerManifestsPath, "identity.yaml")

	if e.RancherConfig.ClusterName == "" {
		e.CattleConfig, err = defaults.SetK8sDefault(e.Client, clusterType, e.CattleConfig)
		require.NoError(t, err)

		resolvedK8sVersion, err := operations.GetValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, e.CattleConfig)
		require.NoError(t, err)

		if resolvedK8sVersion != nil {
			e.CapiConfig.KubernetesVersion, _ = resolvedK8sVersion.(string)
		}

		logrus.Debugf("Applying %s provider manifest...", e.CapiConfig.Provider)
		err = capipnp.CreateProviderManifestFromPath(e.Client, e.CapiConfig, providerManifest)
		require.NoError(t, err)

		logrus.Debugf("Applying credentials manifest...")
		err = capipnp.CreateProviderManifestFromPath(e.Client, e.CapiConfig, credentialsManifest)
		require.NoError(t, err)

		logrus.Debugf("Waiting for previous manifests to be active...")
		err = capipnp.WaitProviderPrerequisitesReady(e.Client, e.CapiConfig)
		require.NoError(t, err)

		logrus.Debugf("Applying cluster static identity manifest...")
		err = capipnp.CreateProviderManifestFromPath(e.Client, e.CapiConfig, identityManifest)
		require.NoError(t, err)

		clusterName := namegen.AppendRandomString(e.CapiConfig.ClusterNamePrefix)

		e.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(e.CapiConfig, e.ClusterConfig.MachinePools, clusterName)
		require.NoError(t, err)

		logrus.Infof("Provisioning %s cluster", clusterType)
		err = capipnp.CreateCAPICluster(e.StandardUserClient, clusterName, e.ClusterManifest)
		require.NoError(t, err)

		cluster, err := clusters.GetClusterByName(e.Client, clusterName)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(e.Client, cluster)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(e.Client, cluster)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(e.Client, cluster)
		require.NoError(t, err)

		e.Cluster, err = e.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + cluster.Name)
		require.NoError(t, err)
	} else {
		e.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(e.CapiConfig, e.ClusterConfig.MachinePools, e.RancherConfig.ClusterName)
		require.NoError(t, err)

		logrus.Infof("Using existing cluster %s", e.RancherConfig.ClusterName)
		e.Cluster, err = e.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + e.RancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return e
}

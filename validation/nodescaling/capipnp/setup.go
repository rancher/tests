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

type nodeScalingTest struct {
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

func Setup(t *testing.T, clusterType string) *nodeScalingTest {
	s := &nodeScalingTest{}

	testSession := session.NewSession()
	s.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	s.Client = client

	localClusterID, err := extclusters.GetClusterIDByName(s.Client, extclusterapi.LocalCluster)
	require.NoError(t, err)

	localCluster, err := s.Client.Management.Cluster.ByID(localClusterID)
	require.NoError(t, err)

	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(s.Client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), localCluster, nil)
	require.NoError(t, err)

	s.StandardUserClient = standardUserClient

	s.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	_, setupFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	capipnpDir := filepath.Dir(setupFile)
	nodescalingDir := filepath.Dir(capipnpDir)
	validationDir := filepath.Dir(nodescalingDir)
	provisioningDir := filepath.Join(validationDir, "provisioning")

	provisioningDefaults := config.LoadConfigFromFile(filepath.Join(provisioningDir, "defaults", "defaults.yaml"))
	nodescalingDefaults := config.LoadConfigFromFile(filepath.Join(nodescalingDir, "defaults", "defaults.yaml"))
	validationDefaults, err := defaults.DeepMerge(nodescalingDefaults, provisioningDefaults, true)
	require.NoError(t, err)

	s.CattleConfig, err = defaults.DeepMerge(s.CattleConfig, validationDefaults, true)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.CattleConfig, clusterConfig)

	s.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.CattleConfig, rancherConfig)

	s.RancherConfig = rancherConfig

	capiConfig := new(capipnp.Config)
	operations.LoadObjectFromMap(capipnp.ConfigurationFileKey, s.CattleConfig, capiConfig)

	s.CapiConfig = capiConfig

	providerManifestsPath := filepath.Join(provisioningDir, "resources", "capipnp", "providers", s.CapiConfig.Provider)
	s.CapiConfig.ClusterYAML = filepath.Join(providerManifestsPath, clusterType+"-cluster.yaml")
	providerManifest := filepath.Join(providerManifestsPath, "provider.yaml")
	credentialsManifest := filepath.Join(providerManifestsPath, "creds.yaml")
	identityManifest := filepath.Join(providerManifestsPath, "identity.yaml")

	if s.RancherConfig.ClusterName == "" {
		s.CattleConfig, err = defaults.SetK8sDefault(s.Client, clusterType, s.CattleConfig)
		require.NoError(t, err)

		resolvedK8sVersion, err := operations.GetValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, s.CattleConfig)
		require.NoError(t, err)

		if resolvedK8sVersion != nil {
			s.CapiConfig.KubernetesVersion, _ = resolvedK8sVersion.(string)
		}

		logrus.Debugf("Applying %s provider manifest...", s.CapiConfig.Provider)
		err = capipnp.CreateProviderManifestFromPath(s.Client, s.CapiConfig, providerManifest)
		require.NoError(t, err)

		logrus.Debugf("Applying credentials manifest...")
		err = capipnp.CreateProviderManifestFromPath(s.Client, s.CapiConfig, credentialsManifest)
		require.NoError(t, err)

		logrus.Debugf("Waiting for previous manifests to be active...")
		err = capipnp.WaitProviderPrerequisitesReady(s.Client, s.CapiConfig)
		require.NoError(t, err)

		logrus.Debugf("Applying cluster static identity manifest...")
		err = capipnp.CreateProviderManifestFromPath(s.Client, s.CapiConfig, identityManifest)
		require.NoError(t, err)

		clusterName := namegen.AppendRandomString(s.CapiConfig.ClusterNamePrefix)

		s.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(s.CapiConfig, s.ClusterConfig.MachinePools, clusterName)
		require.NoError(t, err)

		logrus.Infof("Provisioning %s cluster", clusterType)
		err = capipnp.CreateCAPICluster(s.StandardUserClient, clusterName, s.ClusterManifest)
		require.NoError(t, err)

		cluster, err := clusters.GetClusterByName(s.Client, clusterName)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(s.Client, cluster)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(s.Client, cluster)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(s.Client, cluster)
		require.NoError(t, err)

		s.Cluster, err = s.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + cluster.Name)
		require.NoError(t, err)
	} else {
		s.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(s.CapiConfig, s.ClusterConfig.MachinePools, s.RancherConfig.ClusterName)
		require.NoError(t, err)

		logrus.Infof("Using existing cluster %s", s.RancherConfig.ClusterName)
		s.Cluster, err = s.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + s.RancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return s
}

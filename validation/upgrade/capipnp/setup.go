package capipnp

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	extclusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
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

type upgradeTest struct {
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

func Setup(t *testing.T, clusterType string) *upgradeTest {
	u := &upgradeTest{}

	testSession := session.NewSession()
	u.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	u.Client = client

	localClusterID, err := extclusters.GetClusterIDByName(u.Client, extclusterapi.LocalCluster)
	require.NoError(t, err)

	localCluster, err := u.Client.Management.Cluster.ByID(localClusterID)
	require.NoError(t, err)

	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(u.Client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), localCluster, nil)
	require.NoError(t, err)

	u.StandardUserClient = standardUserClient

	u.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

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

	u.CattleConfig, err = defaults.DeepMerge(u.CattleConfig, validationDefaults, true)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, u.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, u.CattleConfig, clusterConfig)

	u.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, u.CattleConfig, rancherConfig)

	u.RancherConfig = rancherConfig

	capiConfig := new(capipnp.Config)
	operations.LoadObjectFromMap(capipnp.ConfigurationFileKey, u.CattleConfig, capiConfig)

	u.CapiConfig = capiConfig

	providerManifestsPath := filepath.Join(provisioningDir, "resources", "capipnp", "providers", u.CapiConfig.Provider)
	u.CapiConfig.ClusterYAML = filepath.Join(providerManifestsPath, clusterType+"-cluster.yaml")
	providerManifest := filepath.Join(providerManifestsPath, "provider.yaml")
	credentialsManifest := filepath.Join(providerManifestsPath, "creds.yaml")
	identityManifest := filepath.Join(providerManifestsPath, "identity.yaml")

	if u.RancherConfig.ClusterName == "" {
		u.CattleConfig, err = setSecondHighestK8sDefault(u.Client, clusterType, u.CattleConfig)
		require.NoError(t, err)

		resolvedK8sVersion, err := operations.GetValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, u.CattleConfig)
		require.NoError(t, err)

		if resolvedK8sVersion != nil {
			u.CapiConfig.KubernetesVersion, _ = resolvedK8sVersion.(string)
		}

		logrus.Debugf("Applying %s provider manifest...", u.CapiConfig.Provider)
		err = capipnp.CreateProviderManifestFromPath(u.Client, u.CapiConfig, providerManifest)
		require.NoError(t, err)

		logrus.Debugf("Applying credentials manifest...")
		err = capipnp.CreateProviderManifestFromPath(u.Client, u.CapiConfig, credentialsManifest)
		require.NoError(t, err)

		logrus.Debugf("Waiting for previous manifests to be active...")
		err = capipnp.WaitProviderPrerequisitesReady(u.Client, u.CapiConfig)
		require.NoError(t, err)

		logrus.Debugf("Applying cluster static identity manifest...")
		err = capipnp.CreateProviderManifestFromPath(u.Client, u.CapiConfig, identityManifest)
		require.NoError(t, err)

		clusterName := namegen.AppendRandomString(u.CapiConfig.ClusterNamePrefix)

		u.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(u.CapiConfig, u.ClusterConfig.MachinePools, clusterName)
		require.NoError(t, err)

		logrus.Infof("Provisioning %s cluster", clusterType)
		err = capipnp.CreateCAPICluster(u.StandardUserClient, clusterName, u.ClusterManifest)
		require.NoError(t, err)

		cluster, err := clusters.GetClusterByName(u.Client, clusterName)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(u.Client, cluster)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(u.Client, cluster)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(u.Client, cluster)
		require.NoError(t, err)

		u.Cluster, err = u.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + cluster.Name)
		require.NoError(t, err)
	} else {
		u.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(u.CapiConfig, u.ClusterConfig.MachinePools, u.RancherConfig.ClusterName)
		require.NoError(t, err)

		logrus.Infof("Using existing cluster %s", u.RancherConfig.ClusterName)
		u.Cluster, err = u.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + u.RancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return u
}

func setSecondHighestK8sDefault(client *rancher.Client, clusterType string, cattleConfig map[string]any) (map[string]any, error) {
	if clusterConfig, ok := cattleConfig[defaults.ClusterConfigKey].(map[string]any); ok {
		if version, exists := clusterConfig[defaults.K8SVersionKey]; exists && version != "" {
			return cattleConfig, nil
		}
	}

	var versions []string
	var err error

	switch clusterType {
	case extclusters.RKE2ClusterType.String():
		versions, err = kubernetesversions.ListRKE2AllVersions(client)
	case extclusters.K3SClusterType.String():
		versions, err = kubernetesversions.ListK3SAllVersions(client)
	default:
		versions, err = kubernetesversions.Default(client, clusterType, nil)
	}
	if err != nil {
		return nil, err
	}

	if len(versions) < 2 {
		return nil, errors.New("error: expected at least two Kubernetes versions")
	}

	return operations.ReplaceValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, versions[1], cattleConfig)
}

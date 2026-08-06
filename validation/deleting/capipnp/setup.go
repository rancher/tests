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
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/rbac"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type deleteTest struct {
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

func Setup(t *testing.T, clusterType string, defaultNodeRoles bool) *deleteTest {
	d := &deleteTest{}

	testSession := session.NewSession()
	d.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	d.Client = client

	localClusterID, err := extclusters.GetClusterIDByName(d.Client, extclusterapi.LocalCluster)
	require.NoError(t, err)

	localCluster, err := d.Client.Management.Cluster.ByID(localClusterID)
	require.NoError(t, err)

	_, standardUserClient, err := rbac.AddUserWithRoleToCluster(d.Client, rbac.StandardUser.String(), rbac.ClusterOwner.String(), localCluster, nil)
	require.NoError(t, err)

	d.StandardUserClient = standardUserClient

	d.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	_, setupFile, _, ok := runtime.Caller(0)
	require.True(t, ok)

	capipnpDir := filepath.Dir(setupFile)
	deletingDir := filepath.Dir(capipnpDir)
	validationDir := filepath.Dir(deletingDir)
	provisioningDir := filepath.Join(validationDir, "provisioning")

	provisioningDefaults := config.LoadConfigFromFile(filepath.Join(provisioningDir, "defaults", "defaults.yaml"))
	deletingDefaults := config.LoadConfigFromFile(filepath.Join(deletingDir, "defaults", "defaults.yaml"))
	validationDefaults, err := defaults.DeepMerge(deletingDefaults, provisioningDefaults, true)
	require.NoError(t, err)

	d.CattleConfig, err = defaults.DeepMerge(d.CattleConfig, validationDefaults, true)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, d.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.CattleConfig, clusterConfig)

	d.ClusterConfig = clusterConfig

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, d.CattleConfig, rancherConfig)

	d.RancherConfig = rancherConfig

	capiConfig := new(capipnp.Config)
	operations.LoadObjectFromMap(capipnp.ConfigurationFileKey, d.CattleConfig, capiConfig)

	d.CapiConfig = capiConfig

	providerManifestsPath := filepath.Join(provisioningDir, "resources", "capipnp", "providers", d.CapiConfig.Provider)
	d.CapiConfig.ClusterYAML = filepath.Join(providerManifestsPath, clusterType+"-cluster.yaml")
	providerManifest := filepath.Join(providerManifestsPath, "provider.yaml")
	credentialsManifest := filepath.Join(providerManifestsPath, "creds.yaml")
	identityManifest := filepath.Join(providerManifestsPath, "identity.yaml")

	if d.RancherConfig.ClusterName == "" {
		d.CattleConfig, err = defaults.SetK8sDefault(d.Client, clusterType, d.CattleConfig)
		require.NoError(t, err)

		resolvedK8sVersion, err := operations.GetValue([]string{defaults.ClusterConfigKey, defaults.K8SVersionKey}, d.CattleConfig)
		require.NoError(t, err)

		if resolvedK8sVersion != nil {
			d.CapiConfig.KubernetesVersion, _ = resolvedK8sVersion.(string)
		}

		if !defaultNodeRoles {
			nodeRolesStandard := []provisioninginput.MachinePools{
				provisioninginput.EtcdMachinePool,
				provisioninginput.ControlPlaneMachinePool,
				provisioninginput.WorkerMachinePool,
			}

			nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
			nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
			nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

			clusterConfig.MachinePools = nodeRolesStandard
		}

		logrus.Debugf("Applying %s provider manifest...", d.CapiConfig.Provider)
		err = capipnp.CreateProviderManifestFromPath(d.Client, d.CapiConfig, providerManifest)
		require.NoError(t, err)

		logrus.Debugf("Applying credentials manifest...")
		err = capipnp.CreateProviderManifestFromPath(d.Client, d.CapiConfig, credentialsManifest)
		require.NoError(t, err)

		logrus.Debugf("Waiting for previous manifests to be active...")
		err = capipnp.WaitProviderPrerequisitesReady(d.Client, d.CapiConfig)
		require.NoError(t, err)

		logrus.Debugf("Applying cluster static identity manifest...")
		err = capipnp.CreateProviderManifestFromPath(d.Client, d.CapiConfig, identityManifest)
		require.NoError(t, err)

		clusterName := namegen.AppendRandomString(d.CapiConfig.ClusterNamePrefix)

		d.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(d.CapiConfig, d.ClusterConfig.MachinePools, clusterName)
		require.NoError(t, err)

		logrus.Infof("Provisioning %s cluster", clusterType)
		err = capipnp.CreateCAPICluster(d.StandardUserClient, clusterName, d.ClusterManifest)
		require.NoError(t, err)

		cluster, err := clusters.GetClusterByName(d.Client, clusterName)
		require.NoError(t, err)

		err = provisioning.VerifyClusterReady(d.Client, cluster)
		require.NoError(t, err)

		err = deployment.VerifyClusterDeployments(d.Client, cluster)
		require.NoError(t, err)

		err = pods.VerifyClusterPods(d.Client, cluster)
		require.NoError(t, err)

		d.Cluster, err = d.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + cluster.Name)
		require.NoError(t, err)
	} else {
		d.ClusterManifest, err = capipnp.CreateClusterManifestFromDefaults(d.CapiConfig, d.ClusterConfig.MachinePools, d.RancherConfig.ClusterName)
		require.NoError(t, err)

		logrus.Infof("Using existing cluster %s", d.RancherConfig.ClusterName)
		d.Cluster, err = d.Client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + d.RancherConfig.ClusterName)
		require.NoError(t, err)
	}

	return d
}

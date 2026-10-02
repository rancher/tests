package snapshot

import (
	"os"
	"sync"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	provv1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	tfpImported "github.com/rancher/tfp-automation/tests/infrastructure/downstream/imported"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type importedSnapshotTest struct {
	suite.Suite
	Client             *rancher.Client
	StandardUserClient *rancher.Client
	Session            *session.Session
	CattleConfig       map[string]any
	ClusterConfig      *clusters.ClusterConfig
	rancherConfig      *rancher.Config
	Cluster            *v1.SteveAPIObject
	NestedModuleDir    string
	TerraformOptions   *terraform.Options
	Clusters           []ImportedCluster
}

type ImportedCluster struct {
	Name             string
	Cluster          *v1.SteveAPIObject
	NestedModuleDir  string
	TerraformOptions *terraform.Options
}

func ImportedSetup(t *testing.T, clusterType string) *importedSnapshotTest {
	s := &importedSnapshotTest{}

	testSession := session.NewSession()
	s.Session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	s.Client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(s.Client)
	require.NoError(t, err)

	s.StandardUserClient = standardUserClient

	s.CattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.CattleConfig, err = defaults.LoadPackageDefaults(s.CattleConfig, "")
	require.NoError(t, err)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, s.CattleConfig, rancherConfig)

	s.rancherConfig = rancherConfig

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, s.CattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	if rancherConfig.ClusterName == "" {
		nodeRolesAll := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true, Controlplane: true, Worker: true}}
		nodeRolesShared := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true, Controlplane: true}, {Quantity: 1, Worker: true}}
		nodeRolesDedicated := []tfpConfig.Nodepool{{Quantity: 1, Etcd: true}, {Quantity: 1, Controlplane: true}, {Quantity: 1, Worker: true}}
		nodeRolesStandard := []tfpConfig.Nodepool{{Quantity: 3, Etcd: true}, {Quantity: 2, Controlplane: true}, {Quantity: 3, Worker: true}}

		clusterConfigs := []struct {
			name      string
			nodePools []tfpConfig.Nodepool
		}{
			{"etcd_cp_worker", nodeRolesAll},
			{"etcd_cp|worker", nodeRolesShared},
			{"etcd|cp|worker", nodeRolesDedicated},
			{"3_etcd|2_cp|3_worker", nodeRolesStandard},
		}

		s.Clusters = make([]ImportedCluster, len(clusterConfigs))
		var wg sync.WaitGroup
		var mu sync.Mutex
		var errs []error

		for index, topology := range clusterConfigs {
			cattleConfig, err := operations.DeepCopyMap(s.CattleConfig)
			require.NoError(t, err)

			rancherConfig, terraformConfig, terratestConfig, _ := tfpConfig.LoadTFPConfigs(cattleConfig)
			terratestConfig.Nodepools = topology.nodePools

			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				logrus.Infof("Provisioning %s imported cluster (%s)", clusterType, clusterConfigs[index].name)
				moduleDir, options, _, cluster := tfpImported.CreateImportedCluster(t, s.StandardUserClient, rancherConfig, terraformConfig, terratestConfig, clusterType, "validation/provisioning/"+clusterType)

				logrus.Infof("Verifying the imported cluster is ready (%s)", cluster.Name)
				err = provisioning.VerifyClusterReadyV3(s.Client, cluster.Name)
				require.NoError(t, err)

				logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
				err = deployment.VerifyClusterDeployments(s.Client, cluster)
				require.NoError(t, err)

				logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
				err = pods.VerifyClusterPods(s.Client, cluster)
				require.NoError(t, err)

				mu.Lock()
				s.Clusters[index] = ImportedCluster{Name: clusterConfigs[index].name, Cluster: cluster, NestedModuleDir: moduleDir, TerraformOptions: options}
				mu.Unlock()
			}(index)
		}
		wg.Wait()

		for _, err := range errs {
			require.NoError(t, err)
		}

		for _, cluster := range s.Clusters {
			require.NotNil(t, cluster.Cluster, "imported cluster setup did not complete")
		}
	} else {
		logrus.Infof("Using existing cluster (%s)", rancherConfig.ClusterName)
		s.Cluster, err = clusters.GetClusterByName(client, s.rancherConfig.ClusterName)
		require.NoError(t, err)

		s.Clusters = []ImportedCluster{{Name: "existing", Cluster: s.Cluster}}
	}

	for index := range s.Clusters {
		clusterStatus := &provv1.ClusterStatus{}
		err = v1.ConvertToK8sType(s.Clusters[index].Cluster.Status, clusterStatus)
		require.NoError(t, err)
	}

	s.Cluster = s.Clusters[0].Cluster
	s.NestedModuleDir = s.Clusters[0].NestedModuleDir
	s.TerraformOptions = s.Clusters[0].TerraformOptions

	return s
}

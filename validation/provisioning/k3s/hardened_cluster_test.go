//go:build (validation || sanity) && !infra.any && !infra.aks && !infra.eks && !infra.rke2k3s && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !extended && !stress

package k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/clients/rancher/catalog"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/config/operations/permutations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/reports"
	cis "github.com/rancher/tests/validation/provisioning/resources/cisbenchmark"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type HardenedK3SClusterProvisioningTestSuite struct {
	suite.Suite
	client              *rancher.Client
	session             *session.Session
	standardUserClient  *rancher.Client
	cattleConfigs       []map[string]any
	project             *management.Project
	chartInstallOptions *charts.InstallOptions
}

func (c *HardenedK3SClusterProvisioningTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *HardenedK3SClusterProvisioningTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)
	c.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	providerPermutation, err := permutationdata.CreateProviderPermutation(cattleConfig)
	require.NoError(c.T(), err)

	k8sPermutation, err := permutationdata.CreateK8sPermutation(c.client, "k3s", cattleConfig)
	require.NoError(c.T(), err)

	permutedConfigs, err := permutations.Permute([]permutations.Permutation{*k8sPermutation, *providerPermutation}, cattleConfig)
	require.NoError(c.T(), err)

	c.cattleConfigs = append(c.cattleConfigs, permutedConfigs...)

	enabled := true
	var testuser = namegen.AppendRandomString("testuser-")
	var testpassword = password.GenerateUserPassword("testpass-")
	user := &management.User{
		Username: testuser,
		Password: testpassword,
		Name:     testuser,
		Enabled:  &enabled,
	}

	newUser, err := users.CreateUserWithRole(client, user, "user")
	require.NoError(c.T(), err)

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	require.NoError(c.T(), err)

	c.standardUserClient = standardUserClient
}

func (c *HardenedK3SClusterProvisioningTestSuite) TestProvisioningK3SHardenedCluster() {
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name            string
		client          *rancher.Client
		machinePools    []provisioninginput.MachinePools
		scanProfileName string
	}{
		{"K3S_CIS_1.9_Profile|3_etcd|2_cp|3_worker", c.standardUserClient, nodeRolesStandard, "k3s-cis-1.9-profile"},
	}
	for _, tt := range tests {
		for _, cattleConfig := range c.cattleConfigs {

			c.Run(tt.name, func() {
				clusterConfig := new(clusters.ClusterConfig)
				operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

				clusterConfig.MachinePools = tt.machinePools
				clusterConfig.Hardened = true

				externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

				clusterObject, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig)
				reports.TimeoutClusterReport(clusterObject, err)
				require.NoError(c.T(), err)

				provisioning.VerifyCluster(c.T(), tt.client, clusterConfig, clusterObject)

				cluster, err := extensionscluster.NewClusterMeta(tt.client, clusterObject.Name)
				reports.TimeoutClusterReport(clusterObject, err)
				require.NoError(c.T(), err)

				latestCISBenchmarkVersion, err := tt.client.Catalog.GetLatestChartVersion(charts.CISBenchmarkName, catalog.RancherChartRepo)
				require.NoError(c.T(), err)

				project, err := projects.GetProjectByName(tt.client, cluster.ID, cis.System)
				reports.TimeoutClusterReport(clusterObject, err)
				require.NoError(c.T(), err)

				c.project = project
				require.NotEmpty(c.T(), c.project)

				c.chartInstallOptions = &charts.InstallOptions{
					Cluster:   cluster,
					Version:   latestCISBenchmarkVersion,
					ProjectID: c.project.ID,
				}

				cis.SetupCISBenchmarkChart(tt.client, c.project.ClusterID, c.chartInstallOptions, charts.CISBenchmarkNamespace)
				cis.RunCISScan(tt.client, c.project.ClusterID, tt.scanProfileName)
			})
		}

		params, err := provisioning.GetCustomSchemaParams(tt.client, c.cattleConfigs[0])
		require.NoError(c.T(), err)

		err = qase.UpdateSchemaParameters(tt.name, params)
		require.NoError(c.T(), err)
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestHardenedK3SClusterProvisioningTestSuite(t *testing.T) {
	suite.Run(t, new(HardenedK3SClusterProvisioningTestSuite))
}

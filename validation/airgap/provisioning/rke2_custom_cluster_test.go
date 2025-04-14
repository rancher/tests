//go:build airgap

package airgap

import (
	"github.com/rancher/tests/interoperability/airgap"
	"testing"

	apisV1 "github.com/rancher/rancher/pkg/apis/provisioning.cattle.io/v1"
	"github.com/rancher/shepherd/clients/corral"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	steveV1 "github.com/rancher/shepherd/clients/rancher/v1"
	extensionscluster "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioning/permutations"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/reports"
	"github.com/rancher/tests/validation/pipeline/rancherha/corralha"
	"github.com/rancher/tests/validation/provisioning/registries"
	"github.com/rancher/tfp-automation/defaults/configs"
	tfProvision "github.com/rancher/tfp-automation/tests/extensions/provisioning"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type AirGapRKE2CustomClusterTestSuite struct {
	suite.Suite
	client             *rancher.Client
	standardUserClient *rancher.Client
	session            *session.Session
	corralPackage      *corral.Packages
	clustersConfig     *provisioninginput.Config
	registryFQDN       string
}

func (a *AirGapRKE2CustomClusterTestSuite) TearDownSuite() {
	a.session.Cleanup()
}

func (a *AirGapRKE2CustomClusterTestSuite) SetupSuite() {
	testSession := session.NewSession()
	a.session = testSession

	a.clustersConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, a.clustersConfig)

	registriesConfig := new(registries.Registries)
	config.LoadConfig(registries.RegistriesConfigKey, registriesConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(a.T(), err)
	a.client = client

	var testuser = namegen.AppendRandomString("testuser-")
	var testpassword = password.GenerateUserPassword("testpass-")
	enabled := true

	user := &management.User{
		Username: testuser,
		Password: testpassword,
		Name:     testuser,
		Enabled:  &enabled,
	}

	newUser, err := users.CreateUserWithRole(client, user, "user")
	require.NoError(a.T(), err)

	a.standardUserClient, err = client.AsUser(newUser)
	require.NoError(a.T(), err)

	corralRancherHA := new(corralha.CorralRancherHA)
	config.LoadConfig(corralha.CorralRancherHAConfigConfigurationFileKey, corralRancherHA)
	if corralRancherHA.Name != "" {
		a.registryFQDN = airgap.Corral(a.T(), corralRancherHA)
		corralConfig := corral.Configurations()

		err = corral.SetupCorralConfig(corralConfig.CorralConfigVars, corralConfig.CorralConfigUser, corralConfig.CorralSSHPath)
		require.NoError(a.T(), err)

		a.corralPackage = corral.PackagesConfig()
	} else {
		tfRancherHA := new(airgap.TerraformConfig)
		config.LoadConfig(airgap.TerraformConfigurationFileKey, tfRancherHA)
		a.registryFQDN = tfRancherHA.StandaloneAirgapConfig.PrivateRegistry
	}
}

func (a *AirGapRKE2CustomClusterTestSuite) TestProvisioningAirGapRKE2CustomCluster() {
	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	tests := []struct {
		name        string
		client      *rancher.Client
		machinePool []provisioninginput.MachinePools
	}{
		{"1 Node all Roles " + provisioninginput.StandardClientName.String(), a.standardUserClient, nodeRolesAll},
		{"2 nodes - etcd|cp roles per 1 node " + provisioninginput.StandardClientName.String(), a.standardUserClient, nodeRolesShared},
		{"3 nodes - 1 role per node " + provisioninginput.StandardClientName.String(), a.standardUserClient, nodeRolesDedicated},
	}
	for _, tt := range tests {
		a.clustersConfig.MachinePools = tt.machinePool

		if a.clustersConfig.RKE2KubernetesVersions == nil {
			rke2Versions, err := kubernetesversions.ListRKE2AllVersions(a.client)
			require.NoError(a.T(), err)

			a.clustersConfig.RKE2KubernetesVersions = rke2Versions
		}
		if a.corralPackage != nil {
			permutations.RunTestPermutations(&a.Suite, tt.name, tt.client, a.clustersConfig, permutations.RKE2AirgapCluster, nil, a.corralPackage)
		} else {
			cattleConfig, rancherConfig, terraformOptions, terraformConfig, terratestConfig := airgap.TfpSetupSuite(a.T())

			testUser, testPassword := configs.CreateTestCredentials()
			configMap := []map[string]any{cattleConfig}

			module := "airgap_rke2"
			operations.ReplaceValue([]string{"terraform", "module"}, module, configMap[0])
			operations.ReplaceValue([]string{"terraform", "privateRegistries", "systemDefaultRegistry"}, a.registryFQDN, configMap[0])
			operations.ReplaceValue([]string{"terraform", "privateRegistries", "url"}, a.registryFQDN, configMap[0])

			clusterIDs := tfProvision.Provision(a.T(), a.client, rancherConfig, terraformConfig, terratestConfig, testUser, testPassword, terraformOptions, configMap, false)

			tfProvision.VerifyClustersState(a.T(), a.client, clusterIDs)
			tfProvision.VerifyRegistry(a.T(), a.client, clusterIDs[0], terraformConfig)
		}
	}
}

func (a *AirGapRKE2CustomClusterTestSuite) TestProvisioningAirGapUpgradeRKE2CustomCluster() {
	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	tests := []struct {
		name        string
		client      *rancher.Client
		machinePool []provisioninginput.MachinePools
	}{
		{"Upgrading 1 node all Roles from " + provisioninginput.StandardClientName.String(), a.standardUserClient, nodeRolesAll},
		{"Upgrading 2 nodes - etcd|cp roles per 1 node from " + provisioninginput.StandardClientName.String(), a.standardUserClient, nodeRolesShared},
		{"Upgrading 3 nodes - 1 role per node from " + provisioninginput.StandardClientName.String(), a.standardUserClient, nodeRolesDedicated},
	}

	for _, tt := range tests {
		a.clustersConfig.MachinePools = tt.machinePool

		rke2Versions, err := kubernetesversions.ListRKE2AllVersions(a.client)
		require.NoError(a.T(), err)

		require.Equal(a.T(), len(a.clustersConfig.CNIs), 1)

		if a.clustersConfig.RKE2KubernetesVersions != nil {
			rke2Versions = a.clustersConfig.RKE2KubernetesVersions
		}

		numOfRKE2Versions := len(rke2Versions)

		testConfig := clusters.ConvertConfigToClusterConfig(a.clustersConfig)
		testConfig.KubernetesVersion = rke2Versions[numOfRKE2Versions-2]
		testConfig.CNI = a.clustersConfig.CNIs[0]

		versionToUpgrade := rke2Versions[numOfRKE2Versions-1]
		tt.name += testConfig.KubernetesVersion + " to " + versionToUpgrade
		var clusterObject *steveV1.SteveAPIObject

		a.Run(tt.name, func() {
			if a.corralPackage != nil {
				clusterObject, err = provisioning.CreateProvisioningAirgapCustomCluster(a.client, testConfig, a.corralPackage)
				require.NoError(a.T(), err)

				reports.TimeoutClusterReport(clusterObject, err)
				require.NoError(a.T(), err)

				provisioning.VerifyCluster(a.T(), a.client, testConfig, clusterObject)
			} else {
				cattleConfig, rancherConfig, terraformOptions, terraformConfig, terratestConfig := airgap.TfpSetupSuite(a.T())

				testUser, testPassword := configs.CreateTestCredentials()
				configMap := []map[string]any{cattleConfig}

				module := "airgap_rke2"
				operations.ReplaceValue([]string{"terraform", "module"}, module, configMap[0])
				operations.ReplaceValue([]string{"terraform", "privateRegistries", "systemDefaultRegistry"}, a.registryFQDN, configMap[0])
				operations.ReplaceValue([]string{"terraform", "privateRegistries", "url"}, a.registryFQDN, configMap[0])

				clusterIDs := tfProvision.Provision(a.T(), a.client, rancherConfig, terraformConfig, terratestConfig, testUser, testPassword, terraformOptions, configMap, false)

				tfProvision.VerifyClustersState(a.T(), a.client, clusterIDs)
				tfProvision.VerifyRegistry(a.T(), a.client, clusterIDs[0], terraformConfig)
				clusterObject, err = a.client.Steve.SteveType(stevetypes.Provisioning).ByID(airgap.Namespace + "/" + clusterIDs[0])
				require.NoError(a.T(), err)
			}
			updatedClusterObject := new(apisV1.Cluster)

			err = steveV1.ConvertToK8sType(clusterObject, &updatedClusterObject)
			require.NoError(a.T(), err)

			updatedClusterObject.Spec.KubernetesVersion = versionToUpgrade
			testConfig.KubernetesVersion = versionToUpgrade

			a.client, err = a.client.ReLogin()
			require.NoError(a.T(), err)

			upgradedCluster, err := extensionscluster.UpdateK3SRKE2Cluster(a.client, clusterObject, updatedClusterObject)
			require.NoError(a.T(), err)

			provisioning.VerifyCluster(a.T(), a.client, testConfig, upgradedCluster)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestAirGapCustomClusterRKE2ProvisioningTestSuite(t *testing.T) {
	suite.Run(t, new(AirGapRKE2CustomClusterTestSuite))
}

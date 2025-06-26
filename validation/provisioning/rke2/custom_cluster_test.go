//go:build (validation || sanity) && !infra.any && !infra.aks && !infra.eks && !infra.rke2k3s && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !extended && !stress

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/config/operations/permutations"
	"github.com/rancher/shepherd/pkg/environmentflag"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/cloudprovider"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/reports"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type CustomClusterProvisioningTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfigs      []map[string]any
	isWindows          bool
}

func (c *CustomClusterProvisioningTestSuite) TearDownSuite() {
	c.session.Cleanup()
}

func (c *CustomClusterProvisioningTestSuite) SetupSuite() {
	testSession := session.NewSession()
	c.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(c.T(), err)
	c.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	providerPermutation, err := permutationdata.CreateProviderPermutation(cattleConfig)
	require.NoError(c.T(), err)

	k8sPermutation, err := permutationdata.CreateK8sPermutation(c.client, "rke2", cattleConfig)
	require.NoError(c.T(), err)

	cniPermutation, err := permutationdata.CreateCNIPermutation(cattleConfig)
	require.NoError(c.T(), err)

	permutedConfigs, err := permutations.Permute([]permutations.Permutation{*k8sPermutation, *providerPermutation, *cniPermutation}, cattleConfig)
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

func (c *CustomClusterProvisioningTestSuite) TestProvisioningRKE2CustomCluster() {
	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicatedWindows := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool, provisioninginput.WindowsMachinePool}
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
		isWindows    bool
		runFlag      bool
	}{
		{"1 Node all roles " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesAll, false, c.client.Flags.GetValue(environmentflag.Short) || c.client.Flags.GetValue(environmentflag.Long)},
		{"2 nodes - etcd|cp roles per 1 node " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesShared, false, c.client.Flags.GetValue(environmentflag.Short) || c.client.Flags.GetValue(environmentflag.Long)},
		{"3 nodes - 1 role per node " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesDedicated, false, c.client.Flags.GetValue(environmentflag.Long)},
		{"4 nodes - 1 role per node + 1 Windows worker " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesDedicatedWindows, true, c.client.Flags.GetValue(environmentflag.Long)},
		{"8 nodes - 3 etcd, 2 cp, 3 worker " + provisioninginput.StandardClientName.String(), c.standardUserClient, nodeRolesStandard, false, c.client.Flags.GetValue(environmentflag.Long)},
	}
	for _, tt := range tests {
		if !tt.runFlag {
			c.T().Logf("SKIPPED")
			continue
		}

		for _, cattleConfig := range c.cattleConfigs {
			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.machinePools
			name := tt.name + " Node Provider: " + clusterConfig.NodeProvider + " Kubernetes version: " + clusterConfig.KubernetesVersion + " cni: " + clusterConfig.CNI

			c.Run(name, func() {
				externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

				clusterObject, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig)
				reports.TimeoutClusterReport(clusterObject, err)
				require.NoError(c.T(), err)

				provisioning.VerifyCluster(c.T(), tt.client, clusterConfig, clusterObject)
			})
		}
	}
}

func (c *CustomClusterProvisioningTestSuite) TestProvisioningRKE2CustomClusterDynamicInput() {
	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{provisioninginput.AdminClientName.String(), c.client},
		{provisioninginput.StandardClientName.String(), c.standardUserClient},
	}

	for _, tt := range tests {
		c.Run(tt.name, func() {
			for _, cattleConfig := range c.cattleConfigs {
				clusterConfig := new(clusters.ClusterConfig)
				operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)

				if len(clusterConfig.MachinePools) == 0 {
					c.T().Skip()
				}

				externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

				clusterObject, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig)
				reports.TimeoutClusterReport(clusterObject, err)
				require.NoError(c.T(), err)

				provisioning.VerifyCluster(c.T(), tt.client, clusterConfig, clusterObject)
				cloudprovider.VerifyCloudProvider(c.T(), tt.client, "rke2", nil, clusterConfig, clusterObject, nil)
			}
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestCustomClusterRKE2ProvisioningTestSuite(t *testing.T) {
	suite.Run(t, new(CustomClusterProvisioningTestSuite))
}

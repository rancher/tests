//go:build (validation || extended) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke2k3s && !cluster.any && !cluster.custom && !cluster.nodedriver && !sanity && !stress

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/environmentflag"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RKE2CloudProviderTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func (r *RKE2CloudProviderTestSuite) TearDownSuite() {
	r.session.Cleanup()
}

func (r *RKE2CloudProviderTestSuite) SetupSuite() {
	testSession := session.NewSession()
	r.session = testSession

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	client, err := rancher.NewClient("", testSession)
	require.NoError(r.T(), err)
	r.client = client

	r.cattleConfig, err = defaults.SetK8sDefault(client, "rke2", r.cattleConfig)
	require.NoError(r.T(), err)

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
	require.NoError(r.T(), err)

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	require.NoError(r.T(), err)

	r.standardUserClient = standardUserClient
}

func (r *RKE2CloudProviderTestSuite) TestAWSCloudProviderCluster() {
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated[0].MachinePoolConfig.Quantity = 3
	nodeRolesDedicated[1].MachinePoolConfig.Quantity = 2
	nodeRolesDedicated[2].MachinePoolConfig.Quantity = 2

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
		runFlag      bool
	}{
		{"OutOfTree" + provisioninginput.StandardClientName.String(), nodeRolesDedicated, r.standardUserClient, r.client.Flags.GetValue(environmentflag.Long)},
	}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(permutationdata.ClusterConfigKey, r.cattleConfig, clusterConfig)
	if clusterConfig.Provider != "aws" {
		r.T().Skip("AWS Cloud Provider test requires access to aws.")
	}

	for _, tt := range tests {
		if !tt.runFlag {
			r.T().Logf("SKIPPED")
			continue
		}

		clusterConfig.Provider = "aws"
		clusterConfig.MachinePools = tt.machinePools

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
		machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

		clusterObject, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
		require.NoError(r.T(), err)

		provisioning.VerifyCluster(r.T(), tt.client, clusterConfig, clusterObject)
	}
}

func (r *RKE2CloudProviderTestSuite) TestVsphereCloudProviderCluster() {
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated[0].MachinePoolConfig.Quantity = 3
	nodeRolesDedicated[1].MachinePoolConfig.Quantity = 2
	nodeRolesDedicated[2].MachinePoolConfig.Quantity = 2

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
		runFlag      bool
	}{
		{"OutOfTree" + provisioninginput.StandardClientName.String(), nodeRolesDedicated, r.standardUserClient, r.client.Flags.GetValue(environmentflag.Long)},
	}

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(permutationdata.ClusterConfigKey, r.cattleConfig, clusterConfig)
	if clusterConfig.Provider != "vsphere" {
		r.T().Skip("Vsphere Cloud Provider test requires access to vsphere.")
	}

	for _, tt := range tests {
		if !tt.runFlag {
			r.T().Logf("SKIPPED")
			continue
		}

		clusterConfig.CloudProvider = "rancher-vsphere"
		clusterConfig.MachinePools = tt.machinePools

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
		machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

		clusterObject, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
		require.NoError(r.T(), err)

		provisioning.VerifyCluster(r.T(), tt.client, clusterConfig, clusterObject)
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestRKE2CloudProviderTestSuite(t *testing.T) {
	suite.Run(t, new(RKE2CloudProviderTestSuite))
}

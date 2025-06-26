//go:build validation

package k3s

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
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type K3SPSACTTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func (k *K3SPSACTTestSuite) TearDownSuite() {
	k.session.Cleanup()
}

func (k *K3SPSACTTestSuite) SetupSuite() {
	testSession := session.NewSession()
	k.session = testSession

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	client, err := rancher.NewClient("", testSession)
	require.NoError(k.T(), err)

	k.client = client

	k.cattleConfig, err = defaults.SetK8sDefault(client, "k3s", k.cattleConfig)
	require.NoError(k.T(), err)

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
	require.NoError(k.T(), err)

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	require.NoError(k.T(), err)

	k.standardUserClient = standardUserClient
}

func (k *K3SPSACTTestSuite) TestK3SPSACTNodeDriverCluster() {
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		psact        provisioninginput.PSACT
		client       *rancher.Client
	}{
		{"Rancher Privileged " + provisioninginput.StandardClientName.String(), nodeRolesStandard, "rancher-privileged", k.standardUserClient},
		{"Rancher Restricted " + provisioninginput.StandardClientName.String(), nodeRolesStandard, "rancher-restricted", k.standardUserClient},
		{"Rancher Baseline " + provisioninginput.AdminClientName.String(), nodeRolesStandard, "rancher-baseline", k.client},
	}

	for _, tt := range tests {
		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools
		clusterConfig.PSACT = string(tt.psact)

		k.Run(tt.name, func() {
			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			clusterObject, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(k.T(), err)

			provisioning.VerifyCluster(k.T(), tt.client, clusterConfig, clusterObject)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestK3SPSACTTestSuite(t *testing.T) {
	suite.Run(t, new(K3SPSACTTestSuite))
}

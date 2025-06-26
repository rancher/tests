//go:build validation

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

type RKE2PSACTTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func (r *RKE2PSACTTestSuite) TearDownSuite() {
	r.session.Cleanup()
}

func (r *RKE2PSACTTestSuite) SetupSuite() {
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

func (r *RKE2PSACTTestSuite) TestRKE2PSACTNodeDriverCluster() {
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
		{"Rancher Privileged " + provisioninginput.StandardClientName.String(), nodeRolesStandard, "rancher-privileged", r.standardUserClient},
		{"Rancher Restricted " + provisioninginput.StandardClientName.String(), nodeRolesStandard, "rancher-restricted", r.standardUserClient},
		{"Rancher Baseline " + provisioninginput.AdminClientName.String(), nodeRolesStandard, "rancher-baseline", r.client},
	}

	for _, tt := range tests {
		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools
		clusterConfig.PSACT = string(tt.psact)

		r.Run(tt.name, func() {
			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			clusterObject, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			require.NoError(r.T(), err)

			provisioning.VerifyCluster(r.T(), tt.client, clusterConfig, clusterObject)
		})
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestRKE2PSACTTestSuite(t *testing.T) {
	suite.Run(t, new(RKE2PSACTTestSuite))
}

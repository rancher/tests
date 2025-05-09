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
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RKE2ACETestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func (r *RKE2ACETestSuite) TearDownSuite() {
	r.session.Cleanup()
}

func (r *RKE2ACETestSuite) SetupSuite() {
	testSession := session.NewSession()
	r.session = testSession

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	client, err := rancher.NewClient("", testSession)
	require.NoError(r.T(), err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, "rke2", r.cattleConfig)
	require.NoError(r.T(), err)

	r.client = client

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

func (r *RKE2ACETestSuite) TestProvisioningRKE2ClusterACE() {
	nodeRoles0 := []provisioninginput.MachinePools{
		{
			MachinePoolConfig: machinepools.MachinePoolConfig{
				NodeRoles: machinepools.NodeRoles{
					ControlPlane: true,
					Etcd:         false,
					Worker:       false,
					Quantity:     3,
				},
			},
		},
		{
			MachinePoolConfig: machinepools.MachinePoolConfig{
				NodeRoles: machinepools.NodeRoles{
					ControlPlane: false,
					Etcd:         true,
					Worker:       false,
					Quantity:     1,
				},
			},
		},
		{
			MachinePoolConfig: machinepools.MachinePoolConfig{
				NodeRoles: machinepools.NodeRoles{
					ControlPlane: false,
					Etcd:         false,
					Worker:       true,
					Quantity:     1,
				},
			},
		},
	}

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
	}{
		{"Multiple Control Planes - Standard", nodeRoles0, r.standardUserClient},
	}
	// Test is obsolete when ACE is not set.
	for _, tt := range tests {
		subSession := r.session.NewSession()
		defer subSession.Cleanup()

		client, err := tt.client.WithSession(subSession)
		require.NoError(r.T(), err)

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(permutationdata.ClusterConfigKey, r.cattleConfig, clusterConfig)
		require.NotNil(r.T(), clusterConfig.Networking.LocalClusterAuthEndpoint)
		clusterConfig.MachinePools = tt.machinePools

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
		machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

		clusterObject, err := provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
		require.NoError(r.T(), err)

		provisioning.VerifyCluster(r.T(), client, clusterConfig, clusterObject)
	}
}

func TestRKE2ACETestSuite(t *testing.T) {
	suite.Run(t, new(RKE2ACETestSuite))
}

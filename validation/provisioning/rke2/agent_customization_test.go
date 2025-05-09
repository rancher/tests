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

type RKE2AgentCustomizationTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func (r *RKE2AgentCustomizationTestSuite) TearDownSuite() {
	r.session.Cleanup()
}

func (r *RKE2AgentCustomizationTestSuite) SetupSuite() {
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

func (r *RKE2AgentCustomizationTestSuite) TestProvisioningRKE2ClusterAgentCustomization() {
	productionPool := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	productionPool[0].MachinePoolConfig.Quantity = 3
	productionPool[1].MachinePoolConfig.Quantity = 2
	productionPool[2].MachinePoolConfig.Quantity = 2

	agentCustomization := management.AgentDeploymentCustomization{
		AppendTolerations: []management.Toleration{
			{
				Key:   "TestKeyToleration",
				Value: "TestValueToleration",
			},
		},
		OverrideResourceRequirements: &management.ResourceRequirements{
			Limits: map[string]string{
				"cpu": "750m",
				"mem": "500Mi",
			},
			Requests: map[string]string{
				"cpu": "250m",
			},
		},
		OverrideAffinity: &management.Affinity{
			NodeAffinity: &management.NodeAffinity{
				PreferredDuringSchedulingIgnoredDuringExecution: []management.PreferredSchedulingTerm{
					{
						Preference: &management.NodeSelectorTerm{
							MatchExpressions: []management.NodeSelectorRequirement{
								{
									Key:      "testAffinityKey",
									Operator: "In",
									Values:   []string{"true"},
								},
							},
						},
						Weight: 100,
					},
				},
			},
		},
	}

	customAgents := []string{"fleet-agent", "cluster-agent"}
	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
		agent        string
	}{
		{"Custom Fleet Agent - Standard User", productionPool, r.standardUserClient, customAgents[0]},
		{"Custom Cluster Agent - Standard User", productionPool, r.standardUserClient, customAgents[1]},
	}

	for _, tt := range tests {
		subSession := r.session.NewSession()
		defer subSession.Cleanup()

		client, err := tt.client.WithSession(subSession)
		require.NoError(r.T(), err)

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(permutationdata.ClusterConfigKey, r.cattleConfig, clusterConfig)
		clusterConfig.MachinePools = tt.machinePools

		if tt.agent == "fleet-agent" {
			clusterConfig.FleetAgent = &agentCustomization
			clusterConfig.ClusterAgent = nil
		}

		if tt.agent == "cluster-agent" {
			clusterConfig.ClusterAgent = &agentCustomization
			clusterConfig.FleetAgent = nil
		}

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
		machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

		_, err = provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
		require.NoError(r.T(), err)
	}
}

func (r *RKE2AgentCustomizationTestSuite) TestFailureProvisioningRKE2ClusterAgentCustomization() {
	productionPool := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	productionPool[0].MachinePoolConfig.Quantity = 3
	productionPool[1].MachinePoolConfig.Quantity = 2
	productionPool[2].MachinePoolConfig.Quantity = 2

	agentCustomization := management.AgentDeploymentCustomization{
		AppendTolerations: []management.Toleration{
			{
				Key:   "BadLabel",
				Value: "123\"[];'{}-+=",
			},
		},
		OverrideAffinity:             &management.Affinity{},
		OverrideResourceRequirements: &management.ResourceRequirements{},
	}

	customAgents := []string{"fleet-agent", "cluster-agent"}
	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
		agent        string
	}{
		{"Invalid Custom Fleet Agent - Standard User", productionPool, r.standardUserClient, customAgents[0]},
		{"Invalid Custom Cluster Agent - Standard User", productionPool, r.standardUserClient, customAgents[1]},
	}

	for _, tt := range tests {
		subSession := r.session.NewSession()
		defer subSession.Cleanup()

		client, err := tt.client.WithSession(subSession)
		require.NoError(r.T(), err)

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(permutationdata.ClusterConfigKey, r.cattleConfig, clusterConfig)
		clusterConfig.MachinePools = tt.machinePools

		if tt.agent == "fleet-agent" {
			clusterConfig.FleetAgent = &agentCustomization
			clusterConfig.ClusterAgent = nil
		}

		if tt.agent == "cluster-agent" {
			clusterConfig.ClusterAgent = &agentCustomization
			clusterConfig.FleetAgent = nil
		}

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
		machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

		_, err = provisioning.CreateProvisioningCluster(client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
		require.Error(r.T(), err)
	}
}

func TestRKE2AgentCustomizationTestSuite(t *testing.T) {
	suite.Run(t, new(RKE2AgentCustomizationTestSuite))
}

//go:build validation

package rke2

import (
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/users"
	password "github.com/rancher/shepherd/extensions/users/passwordgenerator"
	"github.com/rancher/shepherd/pkg/config"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/provisioning/permutations"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type RKE2PSACTTestSuite struct {
	suite.Suite
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	provisioningConfig *provisioninginput.Config
}

func (r *RKE2PSACTTestSuite) TearDownSuite() {
	r.session.Cleanup()
}

func (r *RKE2PSACTTestSuite) SetupSuite() {
	testSession := session.NewSession()
	r.session = testSession

	r.provisioningConfig = new(provisioninginput.Config)
	config.LoadConfig(provisioninginput.ConfigurationFileKey, r.provisioningConfig)

	client, err := rancher.NewClient("", testSession)
	require.NoError(r.T(), err)

	r.client = client

	if r.provisioningConfig.RKE2KubernetesVersions == nil {
		rke2Versions, err := kubernetesversions.Default(r.client, clusters.RKE2ClusterType.String(), nil)
		require.NoError(r.T(), err)

		r.provisioningConfig.RKE2KubernetesVersions = rke2Versions
	} else if r.provisioningConfig.RKE2KubernetesVersions[0] == "all" {
		rke2Versions, err := kubernetesversions.ListRKE2AllVersions(r.client)
		require.NoError(r.T(), err)

		r.provisioningConfig.RKE2KubernetesVersions = rke2Versions
	}

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
		{"Rancher Privileged " + provisioninginput.AdminClientName.String(), nodeRolesStandard, "rancher-privileged", r.client},
		{"Rancher Restricted " + provisioninginput.AdminClientName.String(), nodeRolesStandard, "rancher-restricted", r.client},
		{"Rancher Baseline " + provisioninginput.AdminClientName.String(), nodeRolesStandard, "rancher-baseline", r.client},
	}

	for _, tt := range tests {
		provisioningConfig := *r.provisioningConfig
		provisioningConfig.MachinePools = tt.machinePools
		provisioningConfig.PSACT = string(tt.psact)

		permutations.RunTestPermutations(&r.Suite, tt.name, tt.client, &provisioningConfig, permutations.RKE2ProvisionCluster, nil, nil)
	}
}

// In order for 'go test' to run this suite, we need to create
// a normal test function and pass our suite to suite.Run
func TestRKE2PSACTTestSuite(t *testing.T) {
	suite.Run(t, new(RKE2PSACTTestSuite))
}

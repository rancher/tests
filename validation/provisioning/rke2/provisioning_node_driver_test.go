//go:build (validation || extended || recurring) && !infra.any && !infra.aks && !infra.eks && !infra.rke2k3s && !infra.gke && !infra.rke1 && !cluster.any && !cluster.custom && !cluster.nodedriver && !sanity && !stress

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
	"github.com/rancher/shepherd/pkg/config/operations/permutations"
	"github.com/rancher/shepherd/pkg/environmentflag"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type RKE2NodeDriverProvisioningTestSuite struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfigs      []map[string]any
}

func setupSuite(t *testing.T) RKE2NodeDriverProvisioningTestSuite {
	var r RKE2NodeDriverProvisioningTestSuite
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)
	r.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	providerPermutation, err := permutationdata.CreateProviderPermutation(cattleConfig)
	assert.NoError(t, err)

	k8sPermutation, err := permutationdata.CreateK8sPermutation(r.client, "rke2", cattleConfig)
	assert.NoError(t, err)

	cniPermutation, err := permutationdata.CreateCNIPermutation(cattleConfig)
	assert.NoError(t, err)

	permutedConfigs, err := permutations.Permute([]permutations.Permutation{*k8sPermutation, *providerPermutation, *cniPermutation}, cattleConfig)
	assert.NoError(t, err)

	r.cattleConfigs = append(r.cattleConfigs, permutedConfigs...)

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
	assert.NoError(t, err)

	newUser.Password = user.Password

	standardUserClient, err := client.AsUser(newUser)
	assert.NoError(t, err)

	r.standardUserClient = standardUserClient

	return r
}

func TestProvisioningRKE2Cluster(t *testing.T) {
	r := setupSuite(t)

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesWindows := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool, provisioninginput.WindowsMachinePool}

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
		isWindows    bool
		runFlag      bool
	}{
		{"1 Node all roles " + provisioninginput.StandardClientName.String(), nodeRolesAll, r.standardUserClient, false, r.client.Flags.GetValue(environmentflag.Short) || r.client.Flags.GetValue(environmentflag.Long)},
		{"2 nodes - etcd|cp roles per 1 node " + provisioninginput.StandardClientName.String(), nodeRolesShared, r.standardUserClient, false, r.client.Flags.GetValue(environmentflag.Short) || r.client.Flags.GetValue(environmentflag.Long)},
		{"3 nodes - 1 role per node " + provisioninginput.StandardClientName.String(), nodeRolesDedicated, r.standardUserClient, false, r.client.Flags.GetValue(environmentflag.Long)},
		{"4 nodes - 1 role per node + 1 Windows worker " + provisioninginput.StandardClientName.String(), nodeRolesWindows, r.standardUserClient, true, r.client.Flags.GetValue(environmentflag.Long)},
	}

	for _, tt := range tests {
		if !tt.runFlag {
			t.Logf("SKIPPED")
			continue
		}

		var err error

		testSession := session.NewSession()
		tt.client, err = tt.client.WithSession(testSession)
		assert.NoError(t, err)

		t.Cleanup(func() {
			logrus.Info("Running cleanup")
			testSession.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, cattleConfig := range r.cattleConfigs {
				clusterConfig := new(clusters.ClusterConfig)
				operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)
				clusterConfig.MachinePools = tt.machinePools

				assert.NotNil(t, clusterConfig.Provider)
				if clusterConfig.Provider != "vsphere" && tt.isWindows {
					t.Skip("Windows test requires access to vsphere")
				}

				provider := provisioning.CreateProvider(clusterConfig.Provider)
				credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
				machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

				cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
				assert.NoError(t, err)

				logrus.Infof("Verifying cluster (%s)", cluster.Name)
				provisioning.VerifyCluster(t, tt.client, clusterConfig, cluster)
			}
		})
	}
}

func TestProvisioningRKE2ClusterDynamicInput(t *testing.T) {
	r := setupSuite(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{provisioninginput.AdminClientName.String(), r.client},
		{provisioninginput.StandardClientName.String(), r.standardUserClient},
	}

	for _, tt := range tests {
		var err error

		testSession := session.NewSession()
		tt.client, err = tt.client.WithSession(testSession)
		assert.NoError(t, err)

		t.Cleanup(func() {
			logrus.Info("Running cleanup")
			testSession.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, cattleConfig := range r.cattleConfigs {
				clusterConfig := new(clusters.ClusterConfig)
				operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)
				if len(clusterConfig.MachinePools) == 0 {
					t.Skip("No machine pools provided")
				}

				provider := provisioning.CreateProvider(clusterConfig.Provider)
				credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
				machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

				cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
				assert.NoError(t, err)

				logrus.Infof("Verifying cluster (%s)", cluster.Name)
				provisioning.VerifyCluster(t, tt.client, clusterConfig, cluster)
			}
		})
	}
}

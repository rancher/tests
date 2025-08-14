//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	namegen "github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type hostnameTruncationTest struct {
	client             *rancher.Client
	session            *session.Session
	cattleConfig       map[string]any
	standardUserClient *rancher.Client
}

func hostnameTruncationSetup(t *testing.T) hostnameTruncationTest {
	var r hostnameTruncationTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	assert.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, defaults.RKE2, r.cattleConfig)
	assert.NoError(t, err)

	r.standardUserClient, err = standard.CreateStandardUser(r.client)
	assert.NoError(t, err)

	return r
}

func TestHostnameTruncation(t *testing.T) {
	t.Parallel()
	r := hostnameTruncationSetup(t)

	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	tests := []struct {
		name                    string
		client                  *rancher.Client
		machinePools            []provisioninginput.MachinePools
		ClusterNameLength       int
		ClusterLengthLimit      int
		machinePoolLengthLimits []int
	}{
		{"RKE2_Hostname_Truncation|10_Characters", r.standardUserClient, nodeRolesDedicated, 63, 10, []int{10, 31, 63}},
		{"RKE2_Hostname_Truncation|31_Characters", r.standardUserClient, nodeRolesDedicated, 63, 31, []int{10, 31, 63}},
		{"RKE2_Hostname_Truncation|63_Characters", r.standardUserClient, nodeRolesDedicated, 63, 63, []int{10, 31, 63}},
	}
	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var hostnamePools []machinepools.HostnameTruncation
			for _, machinePoolLength := range tt.machinePoolLengthLimits {
				currentTruncationPool := machinepools.HostnameTruncation{
					Name:                   namegen.RandStringLower(tt.ClusterNameLength),
					ClusterNameLengthLimit: tt.ClusterLengthLimit,
					PoolNameLengthLimit:    machinePoolLength,
				}

				hostnamePools = append(hostnamePools, currentTruncationPool)
			}

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
			clusterConfig.MachinePools = tt.machinePools

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			clusterObject, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, hostnamePools)
			assert.NoError(t, err)

			provisioning.VerifyCluster(t, r.client, clusterConfig, clusterObject)
			provisioning.VerifyHostnameLength(t, r.client, clusterObject)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

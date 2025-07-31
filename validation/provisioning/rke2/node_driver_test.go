//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
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

type nodeDriverTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func nodeDriverSetup(t *testing.T) nodeDriverTest {
	var r nodeDriverTest
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

func TestNodeDriver(t *testing.T) {
	t.Parallel()
	r := nodeDriverSetup(t)

	nodeRolesAll := []provisioninginput.MachinePools{provisioninginput.AllRolesMachinePool}
	nodeRolesShared := []provisioninginput.MachinePools{provisioninginput.EtcdControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesDedicated := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}
	nodeRolesWindows := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool, provisioninginput.WindowsMachinePool}
	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		machinePools []provisioninginput.MachinePools
		client       *rancher.Client
		isWindows    bool
	}{
		{"RKE2_Node_Driver|etcd_cp_worker", nodeRolesAll, r.standardUserClient, false},
		{"RKE2_Node_Driver|etcd_cp|worker", nodeRolesShared, r.standardUserClient, false},
		{"RKE2_Node_Driver|etcd|cp|worker", nodeRolesDedicated, r.standardUserClient, false},
		{"RKE2_Node_Driver|etcd|cp|worker|windows", nodeRolesWindows, r.standardUserClient, true},
		{"RKE2_Node_Driver|3_etcd|2_cp|3_worker", nodeRolesStandard, r.standardUserClient, false},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Info("Running cleanup")
			r.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
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

			provisioning.VerifyCluster(t, tt.client, clusterConfig, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

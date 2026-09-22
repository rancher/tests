//go:build validation || recurring || pit.weekly || sanity || mixed

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
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
	require.NoError(t, err)
	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	r.cattleConfig, err = defaults.LoadSecretsManagerDefaults(r.cattleConfig)
	require.NoError(t, err)

	err = defaults.VerifyCattleConfig(r.cattleConfig, nil)
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestNodeDriver(t *testing.T) {
	t.Parallel()
	r := nodeDriverSetup(t)
	t.Cleanup(r.session.Cleanup)

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
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resourceSession := session.NewSession()
			clusterSession := session.NewSession()
			t.Cleanup(func() {
				logrus.Infof("Running cleanup (%s)", tt.name)
				clusterSession.Cleanup()
				resourceSession.Cleanup()
			})

			testClient, err := tt.client.WithSession(resourceSession)
			require.NoError(t, err)

			adminClient, err := r.client.WithSession(resourceSession)
			require.NoError(t, err)

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)
			clusterConfig.MachinePools = tt.machinePools

			if clusterConfig.MixedArchitecture && len(tt.machinePools) == 1 && tt.machinePools[0].MachinePoolConfig.Worker && tt.machinePools[0].MachinePoolConfig.Etcd {
				t.Skip("skipping all-roles pool: mixed architecture requires a dedicated worker pool")
			}

			require.NotNil(t, clusterConfig.Provider)

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := provider.LoadMachineConfigFunc(r.cattleConfig)

			if clusterConfig.Provider == "vsphere" && tt.isWindows {
				windowsImage := false
				for _, vmConfig := range machineConfigSpec.VmwareMachineConfigs.VmwarevsphereMachineConfig {
					if vmConfig.OS == "windows" {
						logrus.Info("Windows image found in machine configs")
						windowsImage = true
						break
					}
				}

				if !windowsImage {
					t.Skip("No windows image provided")
				}
			} else if clusterConfig.Provider != "vsphere" && tt.isWindows {
				t.Skip("Windows test requires access to vsphere")
			}

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningClusterWithClusterSession(testClient, provider, credentialSpec, clusterConfig, machineConfigSpec, nil, clusterSession)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(adminClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(testClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(adminClient, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying service account token secret (%s)", cluster.Name)
			err = clusters.VerifyServiceAccountTokenSecret(adminClient, cluster.Name)
			require.NoError(t, err)

			logrus.Infof("Deleting cluster (%s)", cluster.Name)
			err = extClusters.DeleteK3SRKE2Cluster(adminClient, cluster.ID)
			require.NoError(t, err)

			provisioning.VerifyDeleteRKE2K3SCluster(t, adminClient, cluster.ID)
			clusterSession.CleanupEnabled = false
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

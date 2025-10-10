//go:build validation || recurring

package rke2

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	extClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type psactTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
	clusterObjects     []*v1.SteveAPIObject
}

func psactSetup(t *testing.T) psactTest {
	var r psactTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	assert.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	assert.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(client, defaults.RKE2, r.cattleConfig)
	assert.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	assert.NoError(t, err)

	return r
}

func TestPSACT(t *testing.T) {
	t.Parallel()

	r := psactSetup(t)
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
		{"RKE2_Rancher_Privileged|3_etcd|2_cp|3_worker", nodeRolesStandard, "rancher-privileged", r.client},
		{"RKE2_Rancher_Restricted|3_etcd|2_cp|3_worker", nodeRolesStandard, "rancher-restricted", r.client},
		{"RKE2_Rancher_Baseline|3_etcd|2_cp|3_worker", nodeRolesStandard, "rancher-baseline", r.client},
	}

	for _, tt := range tests {
		var err error

		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)

			if len(r.clusterObjects) == 3 {
				for _, cluster := range r.clusterObjects {
					extClusters.DeleteK3SRKE2Cluster(tt.client, cluster.ID)
					provisioning.VerifyDeleteRKE2K3SCluster(t, tt.client, cluster.ID)
				}
			}

			customPSACT, err := tt.client.Steve.SteveType(extClusters.PodSecurityAdmissionSteveResoureType).ByID("rancher-baseline")
			if err == nil && customPSACT != nil {
				_ = clusters.DeletePSACT(tt.client, customPSACT.ID)
			}
		})

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools
		clusterConfig.PSACT = string(tt.psact)

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			assert.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, r.client, cluster)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			pods.VerifyClusterPods(t, r.client, cluster)

			logrus.Infof("Verifying PSACT (%s)", cluster.Name)
			provisioning.VerifyPSACT(t, r.client, cluster)

			r.clusterObjects = append(r.clusterObjects, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, r.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

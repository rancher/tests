//go:build (infra.rke2k3s || validation || recurring) && !infra.any && !infra.aks && !infra.eks && !infra.gke && !infra.rke1 && !stress && !sanity && !extended

package dualstack

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/machines"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

type DeleteMachineDualstackTestSuite struct {
	suite.Suite
	client       *rancher.Client
	session      *session.Session
	cattleConfig map[string]any
	cluster      *v1.SteveAPIObject
}

func (d *DeleteMachineDualstackTestSuite) TearDownSuite() {
	d.session.Cleanup()
}

func (d *DeleteMachineDualstackTestSuite) SetupSuite() {
	testSession := session.NewSession()
	d.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(d.T(), err)

	d.client = client

	standardUserClient, _, _, err := standard.CreateStandardUser(d.client)
	require.NoError(d.T(), err)

	d.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	d.cattleConfig, err = defaults.LoadPackageDefaults(d.cattleConfig, "")
	require.NoError(d.T(), err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, d.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(d.T(), err)

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, d.cattleConfig, clusterConfig)

	rancherConfig := new(rancher.Config)
	operations.LoadObjectFromMap(defaults.RancherConfigKey, d.cattleConfig, rancherConfig)

	if rancherConfig.ClusterName == "" {
		nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

		nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
		nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
		nodeRolesStandard[2].MachinePoolConfig.Quantity = 3
		clusterConfig.MachinePools = nodeRolesStandard

		provider := provisioning.CreateProvider(clusterConfig.Provider)
		machineConfigSpec := provider.LoadMachineConfigFunc(d.cattleConfig)

		logrus.Info("Provisioning RKE2 cluster")
		d.cluster, err = resources.ProvisionRKE2K3SCluster(d.T(), standardUserClient, defaults.RKE2, provider, *clusterConfig, machineConfigSpec, nil, true, false)
		require.NoError(d.T(), err)
	} else {
		logrus.Infof("Using existing cluster %s", rancherConfig.ClusterName)
		d.cluster, err = d.client.Steve.SteveType(stevetypes.Provisioning).ByID("fleet-default/" + rancherConfig.ClusterName)
		require.NoError(d.T(), err)
	}
}

func (d *DeleteMachineDualstackTestSuite) TestDeleteMachine() {
	nodeRolesEtcd := machinepools.NodeRoles{
		Etcd: true,
	}

	nodeRolesControlPlane := machinepools.NodeRoles{
		ControlPlane: true,
	}

	nodeRolesWorker := machinepools.NodeRoles{
		Worker: true,
	}

	tests := []struct {
		name      string
		nodeRoles machinepools.NodeRoles
		cluster   *v1.SteveAPIObject
	}{
		{"RKE2_Replace_Control_Plane", nodeRolesControlPlane, d.cluster},
		{"RKE2_Replace_ETCD", nodeRolesEtcd, d.cluster},
		{"RKE2_Replace_Worker", nodeRolesWorker, d.cluster},
	}

	for _, tt := range tests {
		var err error
		d.Run(tt.name, func() {
			machineList, err := machines.GetMachinesByRole(d.client, tt.cluster, tt.nodeRoles)
			require.NoError(d.T(), err)

			machineToDelete := machineList[0]
			logrus.Infof("Deleting machine (%s) from cluster (%s)", machineToDelete.Name, tt.cluster.Name)
			err = d.client.Steve.SteveType(stevetypes.Machine).Delete(&machineToDelete)
			require.NoError(d.T(), err)

			err = machines.VerifyMachineReplacement(d.client, &machineToDelete)
			require.NoError(d.T(), err)

			logrus.Infof("Verifying cluster is ready after machine replacement (%s)", tt.cluster.Name)
			err = provisioning.VerifyClusterReady(d.client, tt.cluster)
			require.NoError(d.T(), err)

			logrus.Infof("Verifying cluster deployments (%s)", tt.cluster.Name)
			err = deployment.VerifyClusterDeployments(d.client, tt.cluster)
			require.NoError(d.T(), err)

			logrus.Infof("Verifying cluster pods (%s)", tt.cluster.Name)
			err = pods.VerifyClusterPods(d.client, tt.cluster)
			require.NoError(d.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(d.client, d.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestDeleteMachineDualstackTestSuite(t *testing.T) {
	suite.Run(t, new(DeleteMachineDualstackTestSuite))
}

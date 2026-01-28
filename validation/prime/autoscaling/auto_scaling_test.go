//go:build validation || prime

//nolint:forbidigo
package rke2k3s

import (
	"os"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	shepherdClusters "github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/projects"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/scaling"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	resources "github.com/rancher/tests/validation/provisioning/resources/provisioncluster"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type autoScalingTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func autoScalingSetup(t *testing.T) autoScalingTest {
	var s autoScalingTest
	testSession := session.NewSession()
	s.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	s.client = client

	s.standardUserClient, _, _, err = standard.CreateStandardUser(s.client)
	require.NoError(t, err)

	s.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	s.cattleConfig, err = defaults.LoadPackageDefaults(s.cattleConfig, "")
	require.NoError(t, err)

	return s
}

func TestAutoScalingUp(t *testing.T) {
	s := autoScalingSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 1

	tests := []struct {
		name         string
		client       *rancher.Client
		clusterType  string
		nodeRoles    []provisioninginput.MachinePools
		minNodeCount int32
		maxNodeCount int32
	}{
		{"RKE2_Auto_Scale_Up", s.standardUserClient, defaults.RKE2, nodeRolesStandard, 1, 3},
		{"K3S_Auto_Scale_Up", s.standardUserClient, defaults.K3S, nodeRolesStandard, 1, 3},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.nodeRoles
			clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMinSize = &tt.minNodeCount
			clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMaxSize = &tt.maxNodeCount

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

			logrus.Infof("Provisioning %s cluster", tt.clusterType)
			cluster, err := resources.ProvisionRKE2K3SCluster(t, s.client, tt.clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster autoscaler (%s)", cluster.Name)
			scaling.VerifyAutoscaler(t, s.client, cluster)

			v3ClusterID, err := shepherdClusters.GetClusterIDByName(s.client, cluster.Name)
			require.NoError(t, err)

			_, namespace, err := projects.CreateProjectAndNamespace(s.client, v3ClusterID)
			require.NoError(t, err)

			logrus.Info("Creating unscheduleable deployement")
			_, err = deployment.CreateDeployment(s.client, v3ClusterID, namespace.Name, 1000, "", "", false, false, false, false)
			require.NoError(t, err)

			logrus.Infof("Waiting for cluster to scale (%s)", cluster.Name)
			err = scaling.WatchAndWaitForAutoscaling(s.client, cluster, tt.maxNodeCount, time.Minute*10)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(s.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestAutoScalingDown(t *testing.T) {
	t.Skip("Skipped due to https://github.com/rancher/rancher/issues/52665")
	s := autoScalingSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	tests := []struct {
		name         string
		client       *rancher.Client
		clusterType  string
		nodeRoles    []provisioninginput.MachinePools
		minNodeCount int32
		maxNodeCount int32
	}{
		{"RKE2_Auto_Scale_Down", s.standardUserClient, defaults.RKE2, nodeRolesStandard, 1, 3},
		{"K3S_Auto_Scale_Down", s.standardUserClient, defaults.K3S, nodeRolesStandard, 1, 3},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.nodeRoles
			clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMinSize = &tt.minNodeCount
			clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMaxSize = &tt.maxNodeCount

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

			logrus.Infof("Provisioning %s cluster", tt.clusterType)
			cluster, err := resources.ProvisionRKE2K3SCluster(t, tt.client, tt.clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster autoscaler (%s)", cluster.Name)
			scaling.VerifyAutoscaler(t, s.client, cluster)

			v3ClusterID, err := shepherdClusters.GetClusterIDByName(tt.client, cluster.Name)
			require.NoError(t, err)

			_, namespace, err := projects.CreateProjectAndNamespace(tt.client, v3ClusterID)
			require.NoError(t, err)

			logrus.Info("Creating unscheduleable deployement")
			unscheduleableDeployment, err := deployment.CreateDeployment(tt.client, v3ClusterID, namespace.Name, 1000, "", "", false, false, false, false)
			require.NoError(t, err)

			time.Sleep(time.Second * 60)
			logrus.Infof("Deleting deployment (%s)", unscheduleableDeployment.Name)
			err = deployment.DeleteDeployment(tt.client, v3ClusterID, unscheduleableDeployment)
			require.NoError(t, err)

			logrus.Infof("Waiting for cluster to scale (%s)", cluster.Name)
			err = scaling.WatchAndWaitForAutoscaling(tt.client, cluster, tt.minNodeCount, time.Hour*2)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(s.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestAutoScalingPause(t *testing.T) {
	s := autoScalingSetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 1

	tests := []struct {
		name         string
		client       *rancher.Client
		clusterType  string
		nodeRoles    []provisioninginput.MachinePools
		minNodeCount int32
		maxNodeCount int32
	}{
		{"RKE2_Auto_Scale_Pause", s.standardUserClient, defaults.RKE2, nodeRolesStandard, 1, 3},
		{"K3S_Auto_Scale_Pause", s.standardUserClient, defaults.K3S, nodeRolesStandard, 1, 3},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			s.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, s.cattleConfig, clusterConfig)

			clusterConfig.MachinePools = tt.nodeRoles
			clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMinSize = &tt.minNodeCount
			clusterConfig.MachinePools[2].MachinePoolConfig.AutoscalingMaxSize = &tt.maxNodeCount

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			machineConfigSpec := provider.LoadMachineConfigFunc(s.cattleConfig)

			logrus.Infof("Provisioning %s cluster", tt.clusterType)
			cluster, err := resources.ProvisionRKE2K3SCluster(t, s.client, tt.clusterType, provider, *clusterConfig, machineConfigSpec, nil, true, false)
			require.NoError(t, err)

			logrus.Infof("Pausing cluster autoscaler (%s)", cluster.Name)
			err = scaling.UpdateAutoscalerState(s.client, cluster, true)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster autoscaler (%s)", cluster.Name)
			scaling.VerifyAutoscaler(t, s.client, cluster)

			v3ClusterID, err := shepherdClusters.GetClusterIDByName(s.client, cluster.Name)
			require.NoError(t, err)

			_, namespace, err := projects.CreateProjectAndNamespace(s.client, v3ClusterID)
			require.NoError(t, err)

			logrus.Info("Creating unscheduleable deployement")
			_, err = deployment.CreateDeployment(s.client, v3ClusterID, namespace.Name, 1000, "", "", false, false, false, false)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster does not scale (%s)", cluster.Name)
			err = scaling.WatchAndWaitForAutoscaling(s.client, cluster, tt.maxNodeCount, time.Minute*5)
			require.Error(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Unpausing cluster autoscaler (%s)", cluster.Name)
			err = scaling.UpdateAutoscalerState(s.client, cluster, false)
			require.NoError(t, err)

			err = deployment.WaitForDeploymentUpdate(s.client, cluster.ID, namespaces.KubeSystem, scaling.AutoscalerDeploymentName)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster autoscaler (%s)", cluster.Name)
			scaling.VerifyAutoscaler(t, s.client, cluster)

			logrus.Infof("Verifying the cluster scales (%s)", cluster.Name)
			err = scaling.WatchAndWaitForAutoscaling(s.client, cluster, tt.maxNodeCount, time.Minute*5)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(s.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(s.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, s.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}

}

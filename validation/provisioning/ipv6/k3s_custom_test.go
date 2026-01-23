//go:build validation || recurring

package ipv6

import (
	"os"
	"testing"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
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

type customK3SIPv6Test struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func customK3SIPv6Setup(t *testing.T) customK3SIPv6Test {
	var r customK3SIPv6Test

	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, defaults.K3S, r.cattleConfig)
	require.NoError(t, err)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestCustomK3SIPv6(t *testing.T) {
	t.Parallel()
	r := customK3SIPv6Setup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{provisioninginput.EtcdMachinePool, provisioninginput.ControlPlaneMachinePool, provisioninginput.WorkerMachinePool}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

	cidr := &provisioninginput.Networking{
		ClusterCIDR: clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR: clusterConfig.Networking.ServiceCIDR,
	}

	stackPreference := &provisioninginput.Networking{
		ClusterCIDR:     "",
		ServiceCIDR:     "",
		StackPreference: "ipv6",
	}

	cidrStackPreference := &provisioninginput.Networking{
		ClusterCIDR:     clusterConfig.Networking.ClusterCIDR,
		ServiceCIDR:     clusterConfig.Networking.ServiceCIDR,
		StackPreference: "ipv6",
	}

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
		networking   *provisioninginput.Networking
	}{
		{"K3S_IPv6_Custom_CIDR", r.standardUserClient, nodeRolesStandard, cidr},
		{"K3S_IPv6_Custom_Stack_Preference", r.standardUserClient, nodeRolesStandard, stackPreference},
		{"K3S_IPv6_Custom_CIDR_Stack_Preference", r.standardUserClient, nodeRolesStandard, cidrStackPreference},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

		clusterConfig.IPv6Cluster = true
		clusterConfig.MachinePools = tt.machinePools
		clusterConfig.Networking = tt.networking

		for i := range clusterConfig.MachinePools {
			clusterConfig.MachinePools[i].SpecifyCustomPublicIP = true
			clusterConfig.MachinePools[i].SpecifyCustomPrivateIP = true
		}

		if clusterConfig.Advanced == nil {
			clusterConfig.Advanced = &provisioninginput.Advanced{}
		}

		if clusterConfig.Advanced.MachineGlobalConfig == nil {
			clusterConfig.Advanced.MachineGlobalConfig = &rkev1.GenericMap{
				Data: map[string]any{},
			}
		}

		clusterConfig.Advanced.MachineGlobalConfig.Data["flannel-ipv6-masq"] = true

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			provisioning.VerifyClusterReady(t, tt.client, cluster)

			logrus.Infof("Verifying cluster deployments (%s)", cluster.Name)
			err = deployment.VerifyClusterDeployments(tt.client, cluster)
			require.NoError(t, err)

			logrus.Infof("Verifying cluster pods (%s)", cluster.Name)
			err = pods.VerifyClusterPods(tt.client, cluster)
			require.NoError(t, err)
		})

		params := provisioning.GetCustomSchemaParams(tt.client, r.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

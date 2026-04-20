//go:build validation || (recurring && proxy) || proxy

package proxy

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
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type customRKE2ProxyTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
	terraformConfig    *tfpConfig.TerraformConfig
}

func customRKE2ProxySetup(t *testing.T) customRKE2ProxyTest {
	var r customRKE2ProxyTest

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

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, defaults.RKE2, r.cattleConfig)
	require.NoError(t, err)

	r.terraformConfig = new(tfpConfig.TerraformConfig)
	operations.LoadObjectFromMap(tfpConfig.TerraformConfigurationFileKey, r.cattleConfig, r.terraformConfig)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func TestCustomRKE2Proxy(t *testing.T) {
	t.Parallel()
	r := customRKE2ProxySetup(t)

	nodeRolesStandard := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
	}

	nodeRolesDedicatedWindows := []provisioninginput.MachinePools{
		provisioninginput.EtcdMachinePool,
		provisioninginput.ControlPlaneMachinePool,
		provisioninginput.WorkerMachinePool,
		provisioninginput.WindowsMachinePool,
	}

	nodeRolesStandard[0].MachinePoolConfig.Quantity = 3
	nodeRolesStandard[1].MachinePoolConfig.Quantity = 2
	nodeRolesStandard[2].MachinePoolConfig.Quantity = 3

	clusterConfig := new(clusters.ClusterConfig)
	operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

	httpProxy := rkev1.EnvVar{
		Name:  "HTTP_PROXY",
		Value: "http://" + r.terraformConfig.Proxy.ProxyBastion + ":3228",
	}

	httpsProxy := rkev1.EnvVar{
		Name:  "HTTPS_PROXY",
		Value: "http://" + r.terraformConfig.Proxy.ProxyBastion + ":3228",
	}

	noProxy := rkev1.EnvVar{
		Name:  "NO_PROXY",
		Value: "localhost,127.0.0.0/8,10.0.0.0/8,172.0.0.0/8,192.168.0.0/16,.svc,.cluster.local,cattle-system.svc,169.254.169.254",
	}

	clusterConfig.AgentEnvVars = append(clusterConfig.AgentEnvVars, httpProxy, httpsProxy, noProxy)

	tests := []struct {
		name         string
		client       *rancher.Client
		machinePools []provisioninginput.MachinePools
		proxyVars    []rkev1.EnvVar
		isWindows    bool
	}{
		{"RKE2_Proxy_Custom", r.standardUserClient, nodeRolesStandard, []rkev1.EnvVar{httpProxy, httpsProxy, noProxy}, false},
		{"RKE2_Proxy_Custom_Windows", r.standardUserClient, nodeRolesDedicatedWindows, []rkev1.EnvVar{httpProxy, httpsProxy, noProxy}, true},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			r.session.Cleanup()
		})

		clusterConfig := new(clusters.ClusterConfig)
		operations.LoadObjectFromMap(defaults.ClusterConfigKey, r.cattleConfig, clusterConfig)

		clusterConfig.MachinePools = tt.machinePools
		clusterConfig.AgentEnvVars = tt.proxyVars

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

			awsEC2Configs := new(ec2.AWSEC2Configs)
			operations.LoadObjectFromMap(ec2.ConfigurationFileKey, r.cattleConfig, awsEC2Configs)

			if tt.isWindows {
				windowsMachineConfigs := externalNodeProvider.GetWindowsPoolsFunc(tt.client, *awsEC2Configs)
				if len(windowsMachineConfigs) == 0 {
					t.Skip("Windows test requires a windows machine pool")
				}
			}

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
			require.NoError(t, err)

			logrus.Infof("Verifying the cluster is ready (%s)", cluster.Name)
			err = provisioning.VerifyClusterReady(tt.client, cluster)
			require.NoError(t, err)

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

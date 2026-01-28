//go:build validation || dynamic

package k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/config/operations/permutations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/cloudprovider"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/workloads/deployment"
	"github.com/rancher/tests/actions/workloads/pods"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

type DynamicNodeDriverTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfigs      []map[string]any
}

func dynamicNodeDriverSetup(t *testing.T) DynamicNodeDriverTest {
	var k DynamicNodeDriverTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	require.NoError(t, err)
	k.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	providerPermutation, err := permutationdata.CreateProviderPermutation(cattleConfig)
	require.NoError(t, err)

	k8sPermutation, err := permutationdata.CreateK8sPermutation(k.client, defaults.K3S, cattleConfig)
	require.NoError(t, err)

	permutedConfigs, err := permutations.Permute([]permutations.Permutation{*k8sPermutation, *providerPermutation}, cattleConfig)
	require.NoError(t, err)

	k.cattleConfigs = append(k.cattleConfigs, permutedConfigs...)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	require.NoError(t, err)

	return k
}

func TestDynamicNodeDriver(t *testing.T) {
	k := dynamicNodeDriverSetup(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"K3S_Node_Driver|Admin_Client", k.client},
		{"K3S_Node_Driver|Standard_Client", k.standardUserClient},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			for _, cattleConfig := range k.cattleConfigs {
				clusterConfig := new(clusters.ClusterConfig)
				operations.LoadObjectFromMap(defaults.ClusterConfigKey, cattleConfig, clusterConfig)
				if len(clusterConfig.MachinePools) == 0 {
					t.Skip("No machine pools provided")
				}

				provider := provisioning.CreateProvider(clusterConfig.Provider)
				credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
				machineConfigSpec := provider.LoadMachineConfigFunc(cattleConfig)

				logrus.Info("Provisioning cluster")
				cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
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

				logrus.Infof("Verifying cloud provider %s", cluster.Name)
				cloudprovider.VerifyCloudProvider(t, tt.client, defaults.K3S, clusterConfig, cluster, nil)

				logrus.Infof("Verifying cluster features (%s)", cluster.Name)
				provisioning.VerifyDynamicCluster(t, tt.client, cluster)
			}
		})
	}
}

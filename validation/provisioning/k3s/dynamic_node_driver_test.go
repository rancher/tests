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
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/config/permutationdata"
	"github.com/rancher/tests/actions/machinepools"
	"github.com/rancher/tests/actions/provisioning"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
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
	assert.NoError(t, err)
	k.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	providerPermutation, err := permutationdata.CreateProviderPermutation(cattleConfig)
	assert.NoError(t, err)

	k8sPermutation, err := permutationdata.CreateK8sPermutation(k.client, defaults.K3S, cattleConfig)
	assert.NoError(t, err)

	permutedConfigs, err := permutations.Permute([]permutations.Permutation{*k8sPermutation, *providerPermutation}, cattleConfig)
	assert.NoError(t, err)

	k.cattleConfigs = append(k.cattleConfigs, permutedConfigs...)

	k.standardUserClient, err = standard.CreateStandardUser(k.client)
	assert.NoError(t, err)

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
			logrus.Info("Running cleanup")
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
				machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

				cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
				assert.NoError(t, err)

				logrus.Infof("Verifying cluster (%s)", cluster.Name)
				provisioning.VerifyCluster(t, tt.client, clusterConfig, cluster)
			}
		})
	}
}

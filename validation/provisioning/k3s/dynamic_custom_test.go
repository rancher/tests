//go:build validation || dynamic

package k3s

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/ec2"
	"github.com/rancher/shepherd/clients/rancher"
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
	"github.com/rancher/tests/actions/reports"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dynamicCustomTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfigs      []map[string]any
}

func dynamicCustomSetup(t *testing.T) dynamicCustomTest {
	var k dynamicCustomTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)
	k.client = client

	cattleConfig := config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	assert.NoError(t, err)

	providerPermutation, err := permutationdata.CreateProviderPermutation(cattleConfig)
	assert.NoError(t, err)

	k8sPermutation, err := permutationdata.CreateK8sPermutation(k.client, defaults.K3S, cattleConfig)
	assert.NoError(t, err)

	permutedConfigs, err := permutations.Permute([]permutations.Permutation{*k8sPermutation, *providerPermutation}, cattleConfig)
	assert.NoError(t, err)

	k.cattleConfigs = append(k.cattleConfigs, permutedConfigs...)

	k.standardUserClient, _, _, err = standard.CreateStandardUser(k.client)
	assert.NoError(t, err)

	return k
}

func TestDynamicCustom(t *testing.T) {
	k := dynamicCustomSetup(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"K3S_Custom|Admin_Client", k.client},
		{"K3S_Custom|Standard_Client", k.standardUserClient},
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
					t.Skip()
				}

				externalNodeProvider := provisioning.ExternalNodeProviderSetup(clusterConfig.NodeProvider)

				awsEC2Configs := new(ec2.AWSEC2Configs)
				config.LoadConfig(ec2.ConfigurationFileKey, awsEC2Configs)

				logrus.Info("Provisioning cluster")
				cluster, err := provisioning.CreateProvisioningCustomCluster(tt.client, &externalNodeProvider, clusterConfig, awsEC2Configs)
				reports.TimeoutClusterReport(cluster, err)
				require.NoError(t, err)

				logrus.Infof("Verifying cluster (%s)", cluster.Name)
				provisioning.VerifyCluster(t, tt.client, cluster)

				logrus.Infof("Verifying cloud provider %s", cluster.Name)
				cloudprovider.VerifyCloudProvider(t, tt.client, "k3s", nil, clusterConfig, cluster, nil)
			}
		})
	}
}

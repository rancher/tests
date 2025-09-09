//go:build validation || recurring

package k3s

import (
	"os"
	"testing"

	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
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
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

type dataDirectoriesTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
}

func dataDirectoriesSetup(t *testing.T) dataDirectoriesTest {
	var k dataDirectoriesTest
	testSession := session.NewSession()
	k.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)
	k.client = client

	k.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	k.cattleConfig, err = defaults.LoadPackageDefaults(k.cattleConfig, "")
	assert.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, k.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	assert.NoError(t, err)

	k.cattleConfig, err = defaults.SetK8sDefault(k.client, defaults.K3S, k.cattleConfig)
	assert.NoError(t, err)

	k.standardUserClient, err = standard.CreateStandardUser(k.client)
	assert.NoError(t, err)

	return k
}

func TestDataDirectories(t *testing.T) {
	t.Parallel()
	k := dataDirectoriesSetup(t)

	splitDataDirectories := rkev1.DataDirectories{
		SystemAgent:  "/systemAgent",
		Provisioning: "/provisioning",
		K8sDistro:    "/k8sDistro",
	}

	groupedDataDirectories := rkev1.DataDirectories{
		SystemAgent:  "/groupDir/systemAgent",
		Provisioning: "/groupDir/provisioning",
		K8sDistro:    "/groupDir/k8sDistro",
	}

	tests := []struct {
		name            string
		client          *rancher.Client
		dataDirectories rkev1.DataDirectories
	}{
		{"K3S_Split_Data_Directories", k.standardUserClient, splitDataDirectories},
		{"K3S_Grouped_Data_Directories", k.standardUserClient, groupedDataDirectories},
	}

	for _, tt := range tests {
		var err error
		t.Cleanup(func() {
			logrus.Info("Running cleanup")
			k.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			clusterConfig := new(clusters.ClusterConfig)
			operations.LoadObjectFromMap(defaults.ClusterConfigKey, k.cattleConfig, clusterConfig)
			if clusterConfig.Advanced == nil {
				clusterConfig.Advanced = new(provisioninginput.Advanced)
			}
			clusterConfig.Advanced.DataDirectories = &tt.dataDirectories

			provider := provisioning.CreateProvider(clusterConfig.Provider)
			credentialSpec := cloudcredentials.LoadCloudCredential(string(provider.Name))
			machineConfigSpec := machinepools.LoadMachineConfigs(string(provider.Name))

			logrus.Info("Provisioning cluster")
			cluster, err := provisioning.CreateProvisioningCluster(tt.client, provider, credentialSpec, clusterConfig, machineConfigSpec, nil)
			assert.NoError(t, err)

			logrus.Infof("Verifying cluster (%s)", cluster.Name)
			provisioning.VerifyCluster(t, tt.client, cluster)

			logrus.Infof("Verifying cluster (%s) data directories", cluster.Name)
			provisioning.VerifyDataDirectories(t, k.client, clusterConfig, machineConfigSpec, cluster)
		})

		params := provisioning.GetProvisioningSchemaParams(tt.client, k.cattleConfig)
		err = qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

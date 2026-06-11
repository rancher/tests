package registries

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	rkev1 "github.com/rancher/rancher/pkg/apis/rke.cattle.io/v1"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/clusters"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	"github.com/rancher/tests/actions/provisioninginput"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/framework"
	"github.com/rancher/tfp-automation/framework/set/resources/rancher2"

	ranchersetup "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup"
	"github.com/stretchr/testify/require"
)

type registriesTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
	rancherConfig      *rancher.Config
	terraformConfig    *tfpConfig.TerraformConfig
	terratestConfig    *tfpConfig.TerratestConfig
	standaloneConfig   *tfpConfig.Standalone
	terraformOptions   *terraform.Options
}

func registriesSetup(t *testing.T) registriesTest {
	var r registriesTest

	testSession := session.NewSession()
	r.session = testSession

	r.cattleConfig = shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))
	r.rancherConfig, r.terraformConfig, r.terratestConfig, r.standaloneConfig = tfpConfig.LoadTFPConfigs(r.cattleConfig)

	_, keyPath := rancher2.SetKeyPath(keypath.RancherKeyPath, r.terratestConfig.PathToRepo, "")
	terraformOptions := framework.Setup(t, r.terraformConfig, r.terratestConfig, keyPath)

	r.terraformOptions = terraformOptions

	client, err := ranchersetup.PostRancherSetup(t, r.terraformOptions, r.rancherConfig, r.session, r.terraformConfig.Standalone.RancherHostname, keyPath, false)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.terraformConfig = new(tfpConfig.TerraformConfig)
	operations.LoadObjectFromMap(tfpConfig.TerraformConfigurationFileKey, r.cattleConfig, r.terraformConfig)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

func initializeRegistryMachineSelectors(t *testing.T, clusterConfig *clusters.ClusterConfig) {
	if clusterConfig.Advanced == nil {
		clusterConfig.Advanced = &provisioninginput.Advanced{}
	}

	if clusterConfig.Advanced.MachineSelectors == nil {
		clusterConfig.Advanced.MachineSelectors = &[]rkev1.RKESystemConfig{}
	}

	if len(*clusterConfig.Advanced.MachineSelectors) == 0 {
		*clusterConfig.Advanced.MachineSelectors = append(*clusterConfig.Advanced.MachineSelectors, rkev1.RKESystemConfig{})
	}

	if (*clusterConfig.Advanced.MachineSelectors)[0].Config.Data == nil {
		(*clusterConfig.Advanced.MachineSelectors)[0].Config.Data = map[string]interface{}{}
	}
}

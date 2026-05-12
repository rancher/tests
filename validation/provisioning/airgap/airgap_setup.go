package airgap

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/pkg/config"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/logging"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	tfpConfig "github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/framework"
	"github.com/rancher/tfp-automation/framework/set/resources/rancher2"
	"github.com/rancher/tfp-automation/tests/extensions/ssh"
	"github.com/rancher/tfp-automation/tests/infrastructure/ranchers"
	"github.com/stretchr/testify/require"
)

type airgapTest struct {
	client             *rancher.Client
	session            *session.Session
	standardUserClient *rancher.Client
	cattleConfig       map[string]any
	rancherConfig      *rancher.Config
	terraformConfig    *tfpConfig.TerraformConfig
	terratestConfig    *tfpConfig.TerratestConfig
	standaloneConfig   *tfpConfig.Standalone
	terraformOptions   *terraform.Options
	tunnel             *ssh.BastionSSHTunnel
}

func airgapSetup(t *testing.T, clusterType string) airgapTest {
	var r airgapTest

	testSession := session.NewSession()
	r.session = testSession

	r.cattleConfig = shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))
	r.rancherConfig, r.terraformConfig, r.terratestConfig, r.standaloneConfig = tfpConfig.LoadTFPConfigs(r.cattleConfig)

	_, keyPath := rancher2.SetKeyPath(keypath.RancherKeyPath, r.terratestConfig.PathToRepo, "")
	terraformOptions := framework.Setup(t, r.terraformConfig, r.terratestConfig, keyPath)

	r.terraformOptions = terraformOptions

	sshKey, err := os.ReadFile(r.terraformConfig.PrivateKeyPath)
	require.NoError(t, err)

	r.tunnel, err = ssh.StartBastionSSHTunnel(r.terraformConfig.AirgapBastion, r.standaloneConfig.OSUser, sshKey, "8443", r.standaloneConfig.RancherHostname, "443")
	require.NoError(t, err)

	client, err := ranchers.PostRancherSetup(t, r.terraformOptions, r.rancherConfig, r.session, r.terraformConfig.Standalone.RancherHostname, keyPath, false)
	require.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = defaults.LoadPackageDefaults(r.cattleConfig, "")
	require.NoError(t, err)

	loggingConfig := new(logging.Logging)
	operations.LoadObjectFromMap(logging.LoggingKey, r.cattleConfig, loggingConfig)

	err = logging.SetLogger(loggingConfig)
	require.NoError(t, err)

	r.cattleConfig, err = defaults.SetK8sDefault(r.client, clusterType, r.cattleConfig)
	require.NoError(t, err)

	r.terraformConfig = new(tfpConfig.TerraformConfig)
	operations.LoadObjectFromMap(tfpConfig.TerraformConfigurationFileKey, r.cattleConfig, r.terraformConfig)

	r.standardUserClient, _, _, err = standard.CreateStandardUser(r.client)
	require.NoError(t, err)

	return r
}

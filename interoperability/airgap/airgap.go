package airgap

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/token"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/pipeline"
	"github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/framework"
	"github.com/rancher/tfp-automation/framework/set/resources/rancher2"
	"github.com/rancher/tfp-automation/tests/extensions/provisioning"
	"github.com/stretchr/testify/require"
)

func TfpSetupSuite(t *testing.T) (map[string]any, *rancher.Config, *terraform.Options, *config.TerraformConfig, *config.TerratestConfig) {
	testSession := session.NewSession()
	cattleConfig := shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))
	configMap, err := provisioning.UniquifyTerraform([]map[string]any{cattleConfig})
	require.NoError(t, err)

	cattleConfig = configMap[0]
	rancherConfig, terraformConfig, terratestConfig := config.LoadTFPConfigs(cattleConfig)

	adminUser := &management.User{
		Username: "admin",
		Password: rancherConfig.AdminPassword,
	}

	userToken, err := token.GenerateUserToken(adminUser, rancherConfig.Host)
	require.NoError(t, err)

	rancherConfig.AdminToken = userToken.Token

	client, err := rancher.NewClient(rancherConfig.AdminToken, testSession)
	require.NoError(t, err)

	client.RancherConfig.AdminToken = rancherConfig.AdminToken
	client.RancherConfig.AdminPassword = rancherConfig.AdminPassword
	client.RancherConfig.Host = terraformConfig.Standalone.AirgapInternalFQDN

	_, err = operations.ReplaceValue([]string{"rancher", "adminToken"}, rancherConfig.AdminToken, configMap[0])
	require.NoError(t, err)

	_, err = operations.ReplaceValue([]string{"rancher", "adminPassword"}, rancherConfig.AdminPassword, configMap[0])
	require.NoError(t, err)
	_, err = operations.ReplaceValue([]string{"rancher", "host"}, rancherConfig.Host, configMap[0])
	require.NoError(t, err)

	err = pipeline.PostRancherInstall(client, client.RancherConfig.AdminPassword)
	require.NoError(t, err)

	client.RancherConfig.Host = rancherConfig.Host

	_, err = operations.ReplaceValue([]string{"rancher", "host"}, rancherConfig.Host, configMap[0])
	require.NoError(t, err)

	_, keyPath := rancher2.SetKeyPath(keypath.RancherKeyPath, terratestConfig.PathToRepo, "aws")
	terraformOptions := framework.Setup(t, terraformConfig, terratestConfig, keyPath)

	return cattleConfig, rancherConfig, terraformOptions, terraformConfig, terratestConfig
}

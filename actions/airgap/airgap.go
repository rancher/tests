package airgap

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/corral"
	"github.com/rancher/shepherd/clients/rancher"
	management "github.com/rancher/shepherd/clients/rancher/generated/management/v3"
	"github.com/rancher/shepherd/extensions/token"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/pipeline"
	"github.com/rancher/tests/validation/pipeline/rancherha/corralha"
	"github.com/rancher/tests/validation/provisioning/registries"
	"github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	"github.com/rancher/tfp-automation/framework"
	"github.com/rancher/tfp-automation/framework/set/resources/rancher2"
	"github.com/rancher/tfp-automation/tests/extensions/provisioning"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	corralBastionIP    = "bastion_ip"
	corralRegistryIP   = "registry_ip"
	corralRegistryFQDN = "registry_fqdn"
	Namespace          = "fleet-default"
)

func AirgapCorral(t *testing.T, corralRancherHA *corralha.CorralRancherHA) (registryFQDN string) {

	listOfCorrals, err := corral.ListCorral()
	require.NoError(t, err)
	_, corralExist := listOfCorrals[corralRancherHA.Name]
	registriesConfig := new(registries.Registries)

	if corralExist {
		bastionIP, err := corral.GetCorralEnvVar(corralRancherHA.Name, corralRegistryIP)
		require.NoError(t, err)

		err = corral.UpdateCorralConfig(corralBastionIP, bastionIP)
		require.NoError(t, err)

		registryFQDN, err := corral.GetCorralEnvVar(corralRancherHA.Name, corralRegistryFQDN)
		require.NoError(t, err)
		logrus.Infof("registry fqdn is %s", registryFQDN)

		err = corral.SetCorralSSHKeys(corralRancherHA.Name)
		require.NoError(t, err)

		return registryFQDN
	} else {
		registryFQDN = registriesConfig.ExistingNoAuthRegistryURL
	}
	return registryFQDN
}

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

	operations.ReplaceValue([]string{"rancher", "adminToken"}, rancherConfig.AdminToken, configMap[0])
	operations.ReplaceValue([]string{"rancher", "adminPassword"}, rancherConfig.AdminPassword, configMap[0])
	operations.ReplaceValue([]string{"rancher", "host"}, rancherConfig.Host, configMap[0])

	err = pipeline.PostRancherInstall(client, client.RancherConfig.AdminPassword)
	require.NoError(t, err)

	client.RancherConfig.Host = rancherConfig.Host

	operations.ReplaceValue([]string{"rancher", "host"}, rancherConfig.Host, configMap[0])

	keyPath := rancher2.SetKeyPath(keypath.RancherKeyPath)
	terraformOptions := framework.Setup(t, terraformConfig, terratestConfig, keyPath)

	return cattleConfig, rancherConfig, terraformOptions, terraformConfig, terratestConfig
}

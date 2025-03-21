package airgap

import (
	"testing"

	"github.com/rancher/shepherd/clients/corral"
	"github.com/rancher/tests/validation/pipeline/rancherha/corralha"
	"github.com/rancher/tests/validation/provisioning/registries"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	corralPackageAirgapCustomClusterName = "airgapCustomCluster"
	corralBastionIP                      = "bastion_ip"
	corralRegistryIP                     = "registry_ip"
	corralRegistryFQDN                   = "registry_fqdn"
	logMessageKubernetesVersion          = "Validating the current version is the upgraded one"
)

func airgapCorral(t *testing.T, corralRancherHA *corralha.CorralRancherHA) (registryFQDN string) {

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

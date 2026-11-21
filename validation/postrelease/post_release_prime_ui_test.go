//go:build prime

package postrelease

import (
	"testing"

	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/tests/actions/prime"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
)

const (
	sccNamespace   = "cattle-scc-system"
	systemRegistry = "system-default-registry"
	localCluster   = "local"
	uiBrand        = "ui-brand"
	serverVersion  = "server-version"
)

func TestLocalClusterRancherImages(t *testing.T) {
	p := Setup(t, "local")

	tests := []struct {
		name string
	}{
		{"Prime_Local_Cluster_Rancher_Images"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			cluster, err := p.Client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetLocal + "/" + localCluster)
			require.NoError(p.T(), err)

			err = pods.VerifyClusterPods(p.Client, cluster)
			require.NoError(p.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(p.Client, p.CattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestPrimeBrand(t *testing.T) {
	p := Setup(t, "local")

	tests := []struct {
		name string
	}{
		{"Prime_Brand"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			logrus.Infof("Verifying Rancher Prime brand is: %s", p.PrimeConfig.Brand)
			rancherBrand, err := p.Client.Management.Setting.ByID(uiBrand)
			require.NoError(p.T(), err)
			require.Equal(p.T(), p.PrimeConfig.Brand, rancherBrand.Value)
		})

		params := provisioning.GetProvisioningSchemaParams(p.Client, p.CattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestSCCRegistration(t *testing.T) {
	p := Setup(t, "local")

	tests := []struct {
		name string
	}{
		{"Prime_SCC_Registration"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			secret, err := prime.CreateSCCRegistrationSecret(sccNamespace, p.PrimeConfig.SCCRegistrationCode, p.PrimeConfig.SCCRegistrationType)
			require.NoError(p.T(), err)

			sccSecret, err := p.Client.Steve.SteveType("secret").Create(secret)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying SCC registration exists in namespace: %s", sccNamespace)
			sccRegistration, err := p.Client.Steve.SteveType("scc.cattle.io.registration").ListAll(nil)
			require.NoError(p.T(), err)
			require.NotEmpty(p.T(), sccRegistration)

			logrus.Infof("Deleting SCC registration secret in namespace: %s", sccNamespace)
			err = p.Client.Steve.SteveType("secret").Delete(sccSecret)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying SCC registration is deleted in namespace: %s", sccNamespace)
			_, err = p.Client.Steve.SteveType("scc.cattle.io.registration").ListAll(nil)
			require.NoError(p.T(), err)
			require.Empty(p.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(p.Client, p.CattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestSystemDefaultRegistry(t *testing.T) {
	p := Setup(t, "local")

	tests := []struct {
		name string
	}{
		{"Prime_System_Default_Registry"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			logrus.Infof("Verifying system default registry is set to: %s", p.PrimeConfig.Registry)
			registry, err := p.Client.Management.Setting.ByID(systemRegistry)
			require.NoError(p.T(), err)
			require.Equal(p.T(), p.PrimeConfig.Registry, registry.Value)
		})

		params := provisioning.GetProvisioningSchemaParams(p.Client, p.CattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

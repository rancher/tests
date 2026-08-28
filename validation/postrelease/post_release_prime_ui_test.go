//go:build prime

package postrelease

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/prime"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tests/actions/workloads/pods"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupstandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/standard"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	sccNamespace   = "cattle-scc-system"
	systemRegistry = "system-default-registry"
	localCluster   = "local"
	uiBrand        = "ui-brand"
	serverVersion  = "server-version"
)

type PostReleasePrimeUITestSuite struct {
	suite.Suite
	client             *rancher.Client
	standardUserClient *rancher.Client
	session            *session.Session
	cattleConfig       map[string]any
	primeConfig        *prime.Config
}

func (p *PostReleasePrimeUITestSuite) SetupSuite() {
	testSession := session.NewSession()
	p.session = testSession
	p.cattleConfig = shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))

	p.primeConfig = new(prime.Config)
	operations.LoadObjectFromMap(prime.ConfigurationFileKey, p.cattleConfig, p.primeConfig)

	p.client, _, _, _, p.cattleConfig = setupstandard.SetupRancher(p.T(), p.session, keypath.SanityKeyPath, p.cattleConfig)
}

func (p *PostReleasePrimeUITestSuite) TestLocalClusterRancherImages() {
	tests := []struct {
		name string
	}{
		{"Prime_Local_Cluster_Rancher_Images"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			cluster, err := p.client.Steve.SteveType(stevetypes.Provisioning).ByID(namespaces.FleetLocal + "/" + localCluster)
			require.NoError(p.T(), err)

			err = pods.VerifyClusterPods(p.client, cluster)
			require.NoError(p.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(p.client, p.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func (p *PostReleasePrimeUITestSuite) TestPrimeBrand() {
	tests := []struct {
		name string
	}{
		{"Prime_Brand"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			logrus.Infof("Verifying Rancher Prime brand is: %s", p.primeConfig.Brand)
			rancherBrand, err := p.client.Management.Setting.ByID(uiBrand)
			require.NoError(p.T(), err)
			require.Equal(p.T(), p.primeConfig.Brand, rancherBrand.Value)
		})

		params := provisioning.GetProvisioningSchemaParams(p.client, p.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func (p *PostReleasePrimeUITestSuite) TestSCCRegistration() {
	tests := []struct {
		name string
	}{
		{"Prime_SCC_Registration"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			secret, err := prime.CreateSCCRegistrationSecret(sccNamespace, p.primeConfig.SCCRegistrationCode, p.primeConfig.SCCRegistrationType)
			require.NoError(p.T(), err)

			sccSecret, err := p.client.Steve.SteveType("secret").Create(secret)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying SCC registration exists in namespace: %s", sccNamespace)
			sccRegistration, err := p.client.Steve.SteveType("scc.cattle.io.registration").ListAll(nil)
			require.NoError(p.T(), err)
			require.NotEmpty(p.T(), sccRegistration)

			logrus.Infof("Deleting SCC registration secret in namespace: %s", sccNamespace)
			err = p.client.Steve.SteveType("secret").Delete(sccSecret)
			require.NoError(p.T(), err)

			logrus.Infof("Verifying SCC registration is deleted in namespace: %s", sccNamespace)
			_, err = p.client.Steve.SteveType("scc.cattle.io.registration").ListAll(nil)
			require.NoError(p.T(), err)
			require.Empty(p.T(), err)
		})

		params := provisioning.GetProvisioningSchemaParams(p.client, p.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func (p *PostReleasePrimeUITestSuite) TestSystemDefaultRegistry() {
	tests := []struct {
		name string
	}{
		{"Prime_System_Default_Registry"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			logrus.Infof("Verifying system default registry is set to: %s", p.primeConfig.Registry)
			registry, err := p.client.Management.Setting.ByID(systemRegistry)
			require.NoError(p.T(), err)
			require.Equal(p.T(), p.primeConfig.Registry, registry.Value)
		})

		params := provisioning.GetProvisioningSchemaParams(p.client, p.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestPostReleasePrimeUITestSuite(t *testing.T) {
	suite.Run(t, new(PostReleasePrimeUITestSuite))
}

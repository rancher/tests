//go:build validation || prime

package prime

import (
	"os"
	"testing"

	"github.com/rancher/shepherd/clients/rancher"
	"github.com/rancher/shepherd/extensions/rancherversion"
	"github.com/rancher/shepherd/extensions/workloads/pods"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	prime "github.com/rancher/tests/actions/prime"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
)

const (
	systemRegistry = "system-default-registry"
	localCluster   = "local"
	uiBrand        = "ui-brand"
	serverVersion  = "server-version"
)

type PrimeTestSuite struct {
	suite.Suite
	session        *session.Session
	cattleConfig   map[string]any
	client         *rancher.Client
	brand          string
	isPrime        bool
	rancherVersion string
	primeRegistry  string
}

func (t *PrimeTestSuite) TearDownSuite() {
	t.session.Cleanup()
}

func (t *PrimeTestSuite) SetupSuite() {
	testSession := session.NewSession()
	t.session = testSession

	t.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	primeConfig := new(rancherversion.Config)
	config.LoadConfig(rancherversion.ConfigurationFileKey, primeConfig)

	t.brand = primeConfig.Brand
	t.isPrime = primeConfig.IsPrime
	t.rancherVersion = primeConfig.RancherVersion
	t.primeRegistry = primeConfig.Registry

	client, err := rancher.NewClient("", t.session)
	assert.NoError(t.T(), err)

	t.client = client
}

func (t *PrimeTestSuite) TestPrimeBrand() {
	tests := []struct {
		name string
	}{
		{"Prime_Brand"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func() {
			rancherBrand, err := t.client.Management.Setting.ByID(uiBrand)
			require.NoError(t.T(), err)

			checkBrand := prime.CheckUIBrand(t.client, t.isPrime, rancherBrand, t.brand)
			assert.NoError(t.T(), checkBrand)
		})

		params := provisioning.GetProvisioningSchemaParams(t.client, t.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func (t *PrimeTestSuite) TestPrimeVersion() {
	tests := []struct {
		name string
	}{
		{"Prime_Version"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func() {
			serverVersion, err := t.client.Management.Setting.ByID(serverVersion)
			require.NoError(t.T(), err)

			checkVersion := prime.CheckVersion(t.isPrime, t.rancherVersion, serverVersion)
			assert.NoError(t.T(), checkVersion)
		})

		params := provisioning.GetProvisioningSchemaParams(t.client, t.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func (t *PrimeTestSuite) TestSystemDefaultRegistry() {
	tests := []struct {
		name string
	}{
		{"Prime_System_Default_Registry"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func() {
			registry, err := t.client.Management.Setting.ByID(systemRegistry)
			require.NoError(t.T(), err)

			checkRegistry := prime.CheckSystemDefaultRegistry(t.isPrime, t.primeRegistry, registry)
			assert.NoError(t.T(), checkRegistry)
		})

		params := provisioning.GetProvisioningSchemaParams(t.client, t.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func (t *PrimeTestSuite) TestLocalClusterRancherImages() {
	tests := []struct {
		name string
	}{
		{"Prime_Local_Cluster_Rancher_Images"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func() {
			podErrors := pods.StatusPods(t.client, localCluster)
			assert.Empty(t.T(), podErrors)
		})

		params := provisioning.GetProvisioningSchemaParams(t.client, t.cattleConfig)
		err := qase.UpdateSchemaParameters(tt.name, params)
		if err != nil {
			logrus.Warningf("Failed to upload schema parameters %s", err)
		}
	}
}

func TestPrimeTestSuite(t *testing.T) {
	suite.Run(t, new(PrimeTestSuite))
}

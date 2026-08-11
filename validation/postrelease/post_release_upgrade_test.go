//go:build postrelease

package postrelease

import (
	"os"
	"testing"

	"github.com/gruntwork-io/terratest/modules/terraform"
	"github.com/rancher/shepherd/clients/rancher"
	shepherdConfig "github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/qase"
	"github.com/rancher/tfp-automation/config"
	"github.com/rancher/tfp-automation/defaults/keypath"
	setupstandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/setup/standard"
	upgradestandard "github.com/rancher/tfp-automation/tests/infrastructure/ranchers/upgrade/standard"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/suite"
)

type PostReleaseUpgradeTestSuite struct {
	suite.Suite
	client                     *rancher.Client
	session                    *session.Session
	cattleConfig               map[string]any
	terraformConfig            *config.TerraformConfig
	terratestConfig            *config.TerratestConfig
	standaloneTerraformOptions *terraform.Options
	upgradeTerraformOptions    *terraform.Options
	terraformOptions           *terraform.Options
	serverNodeOne              string
}

func (p *PostReleaseUpgradeTestSuite) TestPostReleaseUpgrade() {
	tests := []struct {
		name string
	}{
		{"Post_Release_Upgrade"},
	}

	for _, tt := range tests {
		p.T().Run(tt.name, func(t *testing.T) {
			testSession := session.NewSession()
			p.session = testSession
			p.cattleConfig = shepherdConfig.LoadConfigFromFile(os.Getenv(shepherdConfig.ConfigEnvironmentKey))

			p.client, p.serverNodeOne, p.standaloneTerraformOptions, p.terraformOptions, p.cattleConfig = setupstandard.SetupRancher(p.T(), p.session, keypath.SanityKeyPath, p.cattleConfig)
			p.client, p.cattleConfig, p.terraformOptions, p.upgradeTerraformOptions = upgradestandard.UpgradeRancher(p.T(), p.client, p.serverNodeOne, p.session, p.cattleConfig)
			_, p.terraformConfig, p.terratestConfig, _ = config.LoadTFPConfigs(p.cattleConfig)

			params := provisioning.GetCustomSchemaParams(p.client, p.cattleConfig)
			err := qase.UpdateSchemaParameters(tt.name, params)
			if err != nil {
				logrus.Warningf("Failed to upload schema parameters %s", err)
			}
		})
	}
}

func TestPostReleaseUpgradeTestSuite(t *testing.T) {
	suite.Run(t, new(PostReleaseUpgradeTestSuite))
}

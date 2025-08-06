//go:build validation || recurring

package k3s

import (
	"os"
	"testing"
	"time"

	"github.com/rancher/shepherd/clients/rancher"
	v1 "github.com/rancher/shepherd/clients/rancher/v1"
	"github.com/rancher/shepherd/extensions/cloudcredentials"
	"github.com/rancher/shepherd/extensions/clusters"
	"github.com/rancher/shepherd/extensions/clusters/kubernetesversions"
	"github.com/rancher/shepherd/extensions/defaults"
	"github.com/rancher/shepherd/extensions/defaults/namespaces"
	"github.com/rancher/shepherd/extensions/defaults/stevestates"
	"github.com/rancher/shepherd/extensions/defaults/stevetypes"
	"github.com/rancher/shepherd/extensions/steve"
	"github.com/rancher/shepherd/pkg/config"
	"github.com/rancher/shepherd/pkg/config/operations"
	"github.com/rancher/shepherd/pkg/namegenerator"
	"github.com/rancher/shepherd/pkg/session"
	"github.com/rancher/tests/actions/charts"
	actionsDefaults "github.com/rancher/tests/actions/config/defaults"
	configDefaults "github.com/rancher/tests/actions/config/defaults"
	"github.com/rancher/tests/actions/provisioning"
	"github.com/rancher/tests/actions/provisioninginput"
	standard "github.com/rancher/tests/validation/provisioning/resources/standarduser"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

const (
	localCluster          = "local"
	templateTestConfigKey = "templateTest"
)

type templateTest struct {
	client             *rancher.Client
	standardUserClient *rancher.Client
	session            *session.Session
	templateConfig     *provisioninginput.TemplateConfig
	cloudCredentials   *v1.SteveAPIObject
	cattleConfig       map[string]any
}

func templateSetup(t *testing.T) templateTest {
	var r templateTest
	testSession := session.NewSession()
	r.session = testSession

	client, err := rancher.NewClient("", testSession)
	assert.NoError(t, err)

	r.client = client

	r.cattleConfig = config.LoadConfigFromFile(os.Getenv(config.ConfigEnvironmentKey))

	r.cattleConfig, err = configDefaults.LoadPackageDefaults(r.cattleConfig, "")
	assert.NoError(t, err)

	r.templateConfig = new(provisioninginput.TemplateConfig)
	operations.LoadObjectFromMap(templateTestConfigKey, r.cattleConfig, r.templateConfig)

	provider := provisioning.CreateProvider(r.templateConfig.TemplateProvider)
	cloudCredentialConfig := cloudcredentials.LoadCloudCredential(r.templateConfig.TemplateProvider)
	r.cloudCredentials, err = provider.CloudCredFunc(client, cloudCredentialConfig)
	assert.NoError(t, err)

	r.standardUserClient, err = standard.CreateStandardUser(r.client)
	assert.NoError(t, err)

	return r
}

func TestTemplate(t *testing.T) {
	t.Parallel()
	k := templateSetup(t)

	tests := []struct {
		name   string
		client *rancher.Client
	}{
		{"K3S_Template|etcd|cp|worker", k.standardUserClient},
	}

	for _, tt := range tests {
		t.Cleanup(func() {
			logrus.Infof("Running cleanup (%s)", tt.name)
			k.session.Cleanup()
		})

		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			_, err := steve.CreateAndWaitForResource(k.client, namespaces.FleetLocal+"/"+localCluster, stevetypes.ClusterRepo, k.templateConfig.Repo, stevestates.Active, 5*time.Second, defaults.FiveMinuteTimeout)
			assert.NoError(t, err)

			k8sversions, err := kubernetesversions.Default(k.client, actionsDefaults.K3S, nil)
			assert.NoError(t, err)

			clusterName := namegenerator.AppendRandomString(actionsDefaults.K3S + "-template")
			err = charts.InstallTemplateChart(k.client, k.templateConfig.Repo.ObjectMeta.Name, k.templateConfig.TemplateName, clusterName, k8sversions[0], k.cloudCredentials)
			assert.NoError(t, err)

			_, cluster, err := clusters.GetProvisioningClusterByName(k.client, clusterName, namespaces.FleetDefault)
			assert.NoError(t, err)

			provisioning.VerifyCluster(t, k.client, nil, cluster)
		})
	}
}
